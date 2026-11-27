import { useCallback, useEffect, useRef, useState } from "react";
import {
  Button,
  Alert,
  Card,
  Empty,
  Modal,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  BookOutlined,
  ExclamationCircleOutlined,
  QuestionCircleOutlined,
  SendOutlined,
} from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../api/client";
import type { Draft, ImageDTO, ImageDetail, Page } from "../api/types";
import { useAuth } from "../auth";
import ImageViewer from "../components/ImageViewer";
import DimensionGradeCard from "../components/DimensionGradeCard";
import ExceptionPanel from "../components/ExceptionPanel";
import ExampleDrawer from "../components/ExampleDrawer";
import { dimCommentStates } from "../lib/dimComments";
import { LEVELS, type Level } from "../lib/annotationLevels";
import { exceptionReasonLabel, exceptionReasons } from "../lib/settings";
import { createSubmitId } from "../lib/submitId";
import { STATUS_LABEL } from "../api/types";

interface GroupKindConflict {
  confirmation: string;
  peers: {
    task_id: number;
    group: string;
    kind: "score" | "exception";
    grades: Record<string, Level>;
    dim_comments: Record<string, string>;
    exception_reasons: string[];
    exception_note: string;
  }[];
}

const feedbackLabel = { review: "审核批注", quality: "质检批注", arbitration: "仲裁批注" };

const SHORTCUTS = [
  ["Ctrl / ⌘ + Enter", "提交并下一张"],
  ["← / →", "上一张 / 下一张"],
  ["1 / 2 / 3 / 4 / 5", "为当前维度选择 S / A / B / C / D"],
  ["Tab", "从等级按钮跳到本维评语框"],
  ["A / S / D / F / G", "跳到白平衡、黑平衡、参差、结构、控笔"],
  ["E", "打开异常输入"],
  ["?", "显示快捷键"],
];

export default function AnnotatePage() {
  const { imageId } = useParams();
  return <AnnotationEditor key={imageId} id={Number(imageId)} />;
}

function AnnotationEditor({ id }: { id: number }) {
  const nav = useNavigate();
  const location = useLocation();
  const returnTo = location.state?.returnTo?.startsWith("/workbench?") ? location.state.returnTo : "/workbench";
  const qc = useQueryClient();
  const { me } = useAuth();
  const rubric = me!.rubric;
  const reasons = exceptionReasons(me?.exception_reasons);

  const [grades, setGrades] = useState<Record<string, Level>>({});
  // 五维各自的点评：{ white: "...", black: "..." }
  const [dimComments, setDimComments] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const [activeDim, setActiveDim] = useState(rubric.dims[0]?.key);
  const [invalidDim, setInvalidDim] = useState<string>();
  const cardList = useRef<HTMLDivElement>(null);
  const [excOpen, setExcOpen] = useState(false);
  const [kindConflict, setKindConflict] = useState<{ details: GroupKindConflict; payload: Record<string, unknown> }>();
  const [helpOpen, setHelpOpen] = useState(false);
  const [exampleOpen, setExampleOpen] = useState(false);
  const [initialized, setInitialized] = useState<string | null>(null);
  const startedAt = useRef<number>(Date.now());
  const draftTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const draftSaves = useRef<Promise<unknown>>(Promise.resolve());
  const submitting = useRef(false);

  const detail = useQuery({
    queryKey: ["image", id],
    queryFn: () => api.get<ImageDetail>(`/images/${id}`),
    enabled: Number.isFinite(id) && id > 0,
    refetchInterval: (query) => query.state.data?.image.group_id ? 3000 : false,
    refetchOnWindowFocus: (query) => query.state.data?.image.group_id ? "always" : false,
  });

  const image = detail.data?.image;
  const imageID = image?.id;
  const exceptionLocked = image?.status === "exception_locked";
  const canEdit = !image?.quality_pending && (image?.status === "assigned" || image?.status === "rejected");
  const neighbors = useQuery({
    queryKey: ["task-neighbors", id],
    queryFn: () => api.get<{ prev_id: number | null; next_id: number | null; first_id: number | null }>(`/claims/my/${id}/neighbors`),
    enabled: !!image?.group_id && !!canEdit,
  });
  const queue = useQuery({
    queryKey: ["annotate-queue"],
    enabled: !!image && !image.group_id,
    queryFn: async () => {
      const tasks = async (status: string) => {
        const items: ImageDTO[] = [];
        for (let page = 1; ; page++) {
          const result = await api.get<Page<ImageDTO>>(`/claims/my?status=${status}&page_size=100&page=${page}`);
          items.push(...result.items);
          if (!result.items.length || items.length >= result.total) return items;
        }
      };
      const [active, rejected] = await Promise.all([
        tasks("active"),
        tasks("rejected"),
      ]);
      return [...rejected, ...active];
    },
  });

  useEffect(() => {
    if (!image || initialized === `${image.id}:${image.status}`) return;
    const draft = canEdit ? detail.data?.draft : null;
    const savedGrades = draft?.grades ?? image.grades ?? {};
    setGrades(savedGrades);
    setDimComments(draft?.dim_comments ?? image.dim_comments ?? {});
    setDirty(false);
    setKindConflict(undefined);
    if (!canEdit) setExcOpen(false);
    setInitialized(`${image.id}:${image.status}`);
    startedAt.current = Date.now();
  }, [image, detail.data?.draft, initialized, rubric.dims, canEdit]);

  const dimMin = rubric.dim_comment_min || 10;
  const dimMax = rubric.dim_comment_max || 50;
  const commentStates = dimCommentStates(rubric.dims, dimComments, dimMin, dimMax);
  const firstInvalid = commentStates.find((comment) =>
    !grades[comment.key] || !comment.ok,
  );

  function findCard(key: string) {
    return Array.from(cardList.current?.querySelectorAll<HTMLElement>("[data-dim-comment]") ?? [])
      .find((card) => card.dataset.dimComment === key);
  }

  function pickGrade(key: string, level: Level) {
    const dim = rubric.dims.find((d) => d.key === key);
    if (!dim || !canEdit || excOpen || submitting.current) return false;
    setGrades((previous) => ({ ...previous, [key]: level }));
    setActiveDim(key);
    setInvalidDim((previous) => previous === key ? undefined : previous);
    setDirty(true);
    return true;
  }

  const saveDraft = useCallback(
    (payload: Draft) => {
      if (!canEdit) return Promise.resolve();
      // Serialize autosave and explicit save so an older request cannot win.
      const saved = draftSaves.current.catch(() => undefined).then(async () => {
        await qc.cancelQueries({ queryKey: ["image", id] });
        await api.put(`/images/${id}/draft`, payload);
        await qc.cancelQueries({ queryKey: ["image", id] });
        qc.setQueryData<ImageDetail>(["image", id], (current) => current ? { ...current, draft: payload } : current);
      });
      draftSaves.current = saved;
      return saved;
    },
    [id, qc, canEdit],
  );

  useEffect(() => {
    if (!canEdit || !dirty || !imageID || submitting.current) return;
    draftTimer.current = setTimeout(() => {
      saveDraft({ kind: "score", grades, dim_comments: dimComments }).catch((e) => {
        if (e instanceof ApiError && e.code === "EXCEPTION_LOCKED") {
          void qc.invalidateQueries({ queryKey: ["image", id] });
        }
        message.error(e instanceof ApiError ? e.message : "草稿保存失败，请重试");
      });
    }, 1000);
    return () => clearTimeout(draftTimer.current);
  }, [dirty, grades, dimComments, imageID, saveDraft, canEdit, qc, id]);

  const submit = useMutation({
    mutationFn: async (payload: Record<string, unknown>) => {
      clearTimeout(draftTimer.current);
      await draftSaves.current.catch(() => undefined);
      return api.post<{ peer_annotators?: string[] }>(`/images/${id}/submit`, { ...payload, client_submit_id: createSubmitId() });
    },
    onMutate: () => ({ queue: queue.data ?? [] }),
    onSuccess: async (result, _payload, context) => {
      if (result?.peer_annotators?.length) {
        message.success(`已提交，审核通过后进入仲裁。先提交的标注员：${result.peer_annotators.join("、")}`, 6);
      } else {
        message.success("已提交，等待审核");
      }
      qc.invalidateQueries({ queryKey: ["image", id] });
      qc.invalidateQueries({ queryKey: ["my-counts"] });
      qc.invalidateQueries({ queryKey: ["my-tasks"] });
      setDirty(false);
      if (!me?.limits.auto_next) {
        qc.invalidateQueries({ queryKey: ["annotate-queue"] });
        nav(returnTo);
        return;
      }
      try {
        if (image?.group_id) {
          const refreshed = await neighbors.refetch({ throwOnError: true });
          const nextId = refreshed.data?.next_id ?? refreshed.data?.first_id;
          if (nextId) nav(`/workbench/${nextId}`, { state: location.state });
          else nav(returnTo);
          return;
        }
        const refreshed = await queue.refetch({ throwOnError: true });
        const remaining = (refreshed.data ?? []).filter((item) => item.id !== id);
        const available = new Map(remaining.map((item) => [item.id, item]));
        const previous = context?.queue ?? [];
        const idx = previous.findIndex((item) => item.id === id);
        // Prefer the previous successor if it is still available. At the tail,
        // or if the current image disappeared, use the refreshed queue's head.
        const candidates = idx < 0 ? [] : previous.slice(idx + 1);
        const next = candidates.find((item) => available.has(item.id)) ?? remaining[0];
        if (next) nav(`/workbench/${next.id}`, { state: location.state });
        else nav(returnTo);
      } catch {
        message.warning("已提交，队列刷新失败，请从工作台继续标注");
        nav(returnTo);
      }
    },
    onError: (e, payload) => {
      if (e instanceof ApiError && e.code === "GROUP_KIND_MISMATCH") {
        const details = e.details as GroupKindConflict | undefined;
        if (details?.confirmation && details.peers?.length) {
          setKindConflict({ details, payload });
          return;
        }
      }
      if (e instanceof ApiError && e.code === "EXCEPTION_LOCKED") {
        void qc.invalidateQueries({ queryKey: ["image", id] });
        void qc.invalidateQueries({ queryKey: ["my-tasks"] });
        void qc.invalidateQueries({ queryKey: ["my-counts"] });
      }
      message.error(e instanceof Error ? e.message : "提交失败，请重试");
    },
    onSettled: () => { submitting.current = false; },
  });

  function send(payload: Record<string, unknown>) {
    if (!canEdit || submitting.current) return;
    submitting.current = true;
    submit.mutate(payload);
  }

  function doSubmit() {
    if (!canEdit || excOpen || kindConflict || submitting.current) return;
    if (firstInvalid) {
      const key = firstInvalid.key;
      const commentEnabled = grades[key] !== undefined;
      setActiveDim(key);
      setInvalidDim(key);
      const card = findCard(key);
      const target = card?.querySelector<HTMLElement>(commentEnabled ? "textarea" : '[role="radio"][tabindex="0"]');
      target?.focus({ preventScroll: true });
      card?.scrollIntoView?.({ block: "nearest" });
      message.warning(commentEnabled ? `${firstInvalid.label}评语${firstInvalid.hint}` : `请先为${firstInvalid.label}选档`);
      return;
    }
    send({
      kind: "score",
      grades,
      dim_comments: dimComments,
      duration_ms: Date.now() - startedAt.current,
    });
  }

  const list = queue.data ?? [];
  const idx = list.findIndex((x) => x.id === id);
  const prev = canEdit ? (image?.group_id ? neighbors.data?.prev_id : list[idx - 1]?.id) : undefined;
  const next = canEdit ? (image?.group_id ? neighbors.data?.next_id : idx >= 0 ? list[idx + 1]?.id : undefined) : undefined;

  function go(target?: number | null) {
    if (target) navigateTo(`/workbench/${target}`);
  }

  function navigateTo(path: string) {
    if (submitting.current) return;
    if (canEdit && dirty) {
      const confirm = Modal.confirm({
        title: "当前标注还没提交",
        content: "切换前会把当前内容保存为草稿（仅你可见）。",
        okText: "保存草稿并切换",
        cancelText: "留在本页",
        onOk: (close) => {
          clearTimeout(draftTimer.current);
          confirm.update({ okButtonProps: { loading: true }, cancelButtonProps: { disabled: true } });
          saveDraft({ kind: "score", grades, dim_comments: dimComments }).then(() => {
            close();
            nav(path, { state: location.state });
          }).catch((e) => {
            message.error(e instanceof ApiError ? e.message : "草稿保存失败，请重试");
          }).finally(() => confirm.update({ okButtonProps: { loading: false }, cancelButtonProps: { disabled: false } }));
        },
      });
    } else {
      nav(path, { state: location.state });
    }
  }

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.defaultPrevented || e.isComposing || excOpen || helpOpen || exampleOpen || kindConflict) return;
      const el = e.target as HTMLElement;
      const typing = ["INPUT", "TEXTAREA"].includes(el?.tagName) || el?.isContentEditable;
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
        e.preventDefault();
        doSubmit();
        return;
      }
      if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === "ArrowLeft") go(prev);
      if (e.key === "ArrowRight") go(next);
      if (e.key === "?") setHelpOpen(true);
      if (!canEdit || submitting.current) return;
      const key = e.key.toLowerCase();
      if (key === "e") setExcOpen(true);
      const level = /^[1-5]$/.test(key) ? LEVELS[Number(key) - 1] : undefined;
      if (level && activeDim && pickGrade(activeDim, level)) {
        e.preventDefault();
        const card = findCard(activeDim);
        card?.querySelector<HTMLButtonElement>(`[data-level="${level}"]`)?.focus({ preventScroll: true });
        card?.scrollIntoView?.({ block: "nearest" });
      }
      const dim = rubric.dims[["a", "s", "d", "f", "g"].indexOf(key)];
      if (dim) {
        e.preventDefault();
        findCard(dim.key)?.querySelector<HTMLButtonElement>('[role="radio"][tabindex="0"]')?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (detail.isLoading) {
    return (
      <div style={{ padding: 80, textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }
  if (!image) {
    return <Empty description="图片不存在或已被回收" />;
  }

  const reviews = detail.data?.reviews ?? [];
  const latestReview = reviews[0];
  const feedback = latestReview && (latestReview.comment?.trim() || latestReview.reason_tags?.length) ? latestReview : undefined;
  const exceptionApproval = reviews.find((review) => review.source === "review" && review.action === "approve");
  const exceptionReviews = [latestReview, exceptionApproval === latestReview ? undefined : exceptionApproval]
    .filter((review) => review !== undefined);

  return (
    <div className="annotate-shell">
      <div className="annotate-top">
        <Space className="annotate-meta" size={[14, 6]} wrap>
          <Button aria-label="返回列表" icon={<ArrowLeftOutlined />} onClick={() => navigateTo(returnTo)} disabled={submit.isPending}>返回列表</Button>
          <Tag className="annotate-folder" color="blue">{image.folder}</Tag>
          <Typography.Text className="annotate-filename" strong>{image.stored_name}</Typography.Text>
          <Typography.Text type="secondary">
            第 {idx >= 0 ? idx + 1 : 1} / {list.length || 1} 张
          </Typography.Text>
          {image.quality_pending && <Tag color="orange">待质检或仲裁</Tag>}
          {image.status === "rejected" && !image.quality_pending && <Tag color="error">被打回 {image.reject_count} 次</Tag>}
        </Space>
        <Space className="annotate-actions">
          <Tooltip title="上一张 (←)">
            <Button aria-label="上一张" icon={<ArrowLeftOutlined />} disabled={!prev || submit.isPending} onClick={() => go(prev)} />
          </Tooltip>
          <Tooltip title="下一张 (→)">
            <Button aria-label="下一张" icon={<ArrowRightOutlined />} disabled={!next || submit.isPending} onClick={() => go(next)} />
          </Tooltip>
          <Button icon={<BookOutlined />} onClick={() => setExampleOpen(true)}>
            查看示例
          </Button>
          <Button aria-label="快捷键" icon={<QuestionCircleOutlined />} onClick={() => setHelpOpen(true)} />
        </Space>
      </div>

      <div className="annotate-body">
        <Card className="canvas-card" styles={{ body: { padding: 0, display: "flex", flex: 1 } }}>
          <ImageViewer src={image.original_url || image.preview_url} alt={image.stored_name} resetKey={image.id} />
        </Card>

        <div className="panel-card">
          <div className="panel-scroll">
          <Alert className="annotation-status" type="info" showIcon title={
            <div className="annotation-status-text">
              <strong>{canEdit
                ? `${image.status === "rejected" ? "已打回" : "待标注"} · 可编辑`
                : exceptionLocked ? "对组异常已确认 · 无需标注"
                : `${image.quality_pending ? "待质检或仲裁" : STATUS_LABEL[image.status] || image.status} · 只读`}</strong>
              {canEdit && <span>提交后只读，审核退回后可修改。</span>}
              {!canEdit && !!detail.data?.peer_annotators?.length && (
                <span>先提交的标注员：{detail.data.peer_annotators.join("、")}</span>
              )}
            </div>
          } />
          {image.kind === "exception" && (
            <Alert type="warning" showIcon title={exceptionLocked ? "对组判定的异常" : "照片不合格原因"} style={{ marginBottom: 12 }} description={
              <Space orientation="vertical" size={4}>
                {(image.exception_reasons ?? []).map((reason) => <span key={reason}>{exceptionReasonLabel(reason, reasons)}</span>)}
                {image.comment && <span>备注：{image.comment}</span>}
              </Space>
            } />
          )}
          {exceptionLocked && exceptionReviews.map((review) => (
            <Alert
              key={`${review.source}:${review.id}`}
              type={review.action === "approve" ? "success" : "warning"}
              style={{ marginBottom: 12 }}
              title={`${feedbackLabel[review.source ?? "review"].replace("批注", "")}${review.action === "approve" ? "通过" : "退回"} · ${review.reviewer}`}
              description={
                <Space orientation="vertical" size={4}>
                  <span>{new Date(review.created_at).toLocaleString("zh-CN")}</span>
                  {review === feedback && review.comment && <span>{review.comment}</span>}
                  {review === feedback && review.reason_tags?.length > 0 && <span>{review.reason_tags.join("、")}</span>}
                </Space>
              }
            />
          ))}
          {!exceptionLocked && feedback && (
            <div className="reject-card">
              <div className="who">
                {feedbackLabel[feedback.source ?? "review"]} · {feedback.reviewer} ·{" "}
                {new Date(feedback.created_at).toLocaleString("zh-CN")}
              </div>
              <div className="body">{feedback.comment}</div>
              {feedback.reason_tags?.length > 0 && (
                <Space size={4} wrap style={{ marginTop: 8 }}>
                  {feedback.reason_tags.map((t) => (
                    <Tag key={t} color="red">
                      {t}
                    </Tag>
                  ))}
                </Space>
              )}
            </div>
          )}

          {!exceptionLocked && <>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center", marginBottom: 10 }}>
            <Tag className="annotate-folder" color="blue">{image.folder}</Tag>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              序号 {image.seq} · {STATUS_LABEL[image.status] || image.status}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12, maxWidth: 210, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={image.orig_name}>
              原图：{image.orig_name}
            </Typography.Text>
          </div>

          {canEdit && (
            <Alert
              className="annotation-photo-check"
              type="info"
              title="照片质量检查"
              style={{ marginBottom: 12 }}
              description={
                <>
                  <div>定级前检查是否有错字、无法辨认、空白、多字、非书写内容，或图片模糊到看不清笔画。</div>
                  <div>拍摄角度、光线和遮挡以能否看清笔画为准。能辨认的单字按五维定级。</div>
                  <details className="photo-quality-example">
                    <summary>查看不合格照片示例</summary>
                    <figure className="guide-fig">
                      <img src="/examples/invalid.png" alt="不合格照片示例" loading="lazy" />
                      <figcaption className="cap">这张照片因失焦和遮挡而看不清笔画。</figcaption>
                    </figure>
                  </details>
                </>
              }
            />
          )}

          <div className="dimension-card-list" ref={cardList} aria-disabled={excOpen || submit.isPending}>
            {rubric.dims.map((dim, index) => (
              <DimensionGradeCard key={dim.key} dim={dim} grade={grades[dim.key]} reviewChange={detail.data?.grade_changes?.[dim.key]}
                comment={commentStates[index]} templates={me?.comment_templates} minLength={dimMin} maxLength={dimMax}
                editable={canEdit} frozen={excOpen || submit.isPending}
                active={canEdit && activeDim === dim.key} invalid={invalidDim === dim.key}
                onActivate={() => setActiveDim(dim.key)} onPick={(level) => { pickGrade(dim.key, level); }}
                onComment={(value) => {
                  setDimComments((previous) => ({ ...previous, [dim.key]: value }));
                  setInvalidDim((previous) => previous === dim.key ? undefined : previous);
                  setDirty(true);
                }}
              />
            ))}
          </div>
          </>}

          </div>

          {!exceptionLocked && <div className="panel-footer">
            <Tooltip title="提交并进入下一张（⌘/Ctrl + Enter）">
              <span style={{ flex: 1, display: "inline-flex" }}>
                <Button
                  type="primary"
                  size="large"
                  style={{ width: "100%" }}
                  icon={<SendOutlined />}
                  loading={submit.isPending}
                  disabled={!canEdit || excOpen}
                  onClick={doSubmit}
                  data-test="submit"
                >
                  提交并下一张
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="选择异常类型并提交审核（E）">
              <Button
                disabled={!canEdit || submit.isPending}
                danger
                size="large"
                icon={<ExclamationCircleOutlined aria-hidden="true" />}
                aria-label="异常输入"
                onClick={() => setExcOpen(true)}
                data-test="exception"
              >
                异常输入
              </Button>
            </Tooltip>
          </div>}
        </div>
      </div>

      <ExceptionPanel
        open={canEdit && excOpen && !kindConflict}
        reasons={reasons}
        loading={submit.isPending}
        onCancel={() => setExcOpen(false)}
        onSubmit={(reason) =>
          send({
            kind: "exception",
            exception_reasons: [reason],
            duration_ms: Date.now() - startedAt.current,
          })
        }
      />

      <ExampleDrawer open={exampleOpen} onClose={() => setExampleOpen(false)} />

      <Modal
        open={canEdit && !!kindConflict}
        title="组间有效性判定不同"
        okText="确认并提交"
        cancelText="返回修改"
        confirmLoading={submit.isPending}
        onCancel={() => setKindConflict(undefined)}
        onOk={() => {
          if (!kindConflict) return;
          const { details, payload } = kindConflict;
          setKindConflict(undefined);
          setExcOpen(false);
          send({ ...payload, group_confirmation: details.confirmation });
        }}
      >
        <Alert type="warning" showIcon title={kindConflict?.payload.kind === "exception"
          ? "本图将被判为无效。异常审核通过后，对方标注将作废。"
          : "对组已提交异常。若异常审核通过，本组的评级将作废。"} />
        {kindConflict?.details.peers.map((peer) => (
          <Card key={peer.task_id} size="small" title={peer.group} style={{ marginTop: 12 }}>
            {peer.kind === "score" ? rubric.dims.map(({ key, label }) => (
              <div key={key}><strong>{label}</strong> <Tag>{peer.grades[key] ?? "—"}</Tag>{peer.dim_comments[key]}</div>
            )) : <>
              <div>{peer.exception_reasons.map((key) => exceptionReasonLabel(key, reasons)).join("、")}</div>
              {peer.exception_note && <div>{peer.exception_note}</div>}
            </>}
          </Card>
        ))}
      </Modal>

      <Modal open={helpOpen} footer={null} onCancel={() => setHelpOpen(false)} title="快捷键">
        {SHORTCUTS.map(([k, v]) => (
          <div key={k} style={{ display: "flex", justifyContent: "space-between", padding: "6px 0" }}>
            <span className="kbd">{k}</span>
            <span>{v}</span>
          </div>
        ))}
      </Modal>
    </div>
  );
}
