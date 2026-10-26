import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Card,
  Dropdown,
  Empty,
  Input,
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
import { useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../api/client";
import type { Draft, ImageDTO, ImageDetail, Page, Review } from "../api/types";
import { useAuth } from "../auth";
import ImageViewer from "../components/ImageViewer";
import ScorePanel, { computeTotal, snapToStep } from "../components/ScorePanel";
import ExceptionPanel from "../components/ExceptionPanel";
import ExampleDrawer from "../components/ExampleDrawer";
import { dimCommentStates, dimCommentsOk, firstBlockingDimComment } from "../lib/dimComments";
import { createSubmitId } from "../lib/submitId";

const SHORTCUTS = [
  ["Ctrl / ⌘ + Enter", "提交并下一张"],
  ["← / →", "上一张 / 下一张"],
  ["A / S / D / F / G", "跳到 白平衡 / 黑平衡 / 参差 / 结构 / 控笔 的分数框"],
  ["E", "上报异常"],
  ["?", "显示快捷键"],
];

export default function AnnotatePage() {
  const { imageId } = useParams();
  return <AnnotationEditor key={imageId} id={Number(imageId)} />;
}

function AnnotationEditor({ id }: { id: number }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const { me } = useAuth();
  const rubric = me!.rubric;

  const [scores, setScores] = useState<Record<string, number>>({});
  // 五维各自的点评：{ white: "...", black: "..." }
  const [dimComments, setDimComments] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);
  const [hotkeyDim, setHotkeyDim] = useState(rubric.dims[0]?.key ?? "fill");
  const [excOpen, setExcOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [exampleOpen, setExampleOpen] = useState(false);
  const [shortcutSubmit, setShortcutSubmit] = useState(false);
  const [initialized, setInitialized] = useState<number | null>(null);
  const startedAt = useRef<number>(Date.now());
  const draftTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const draftSaves = useRef<Promise<unknown>>(Promise.resolve());
  const submitting = useRef(false);

  const detail = useQuery({
    queryKey: ["image", id],
    queryFn: () => api.get<ImageDetail>(`/images/${id}`),
    enabled: Number.isFinite(id) && id > 0,
  });

  const queue = useQuery({
    queryKey: ["annotate-queue"],
    queryFn: async () => {
      const [active, rejected] = await Promise.all([
        api.get<Page<ImageDTO>>("/claims/my?status=active&page_size=200"),
        api.get<Page<ImageDTO>>("/claims/my?status=rejected&page_size=200"),
      ]);
      return [...rejected.items, ...active.items];
    },
  });

  const image = detail.data?.image;

  useEffect(() => {
    if (!image || initialized === image.id) return;
    const draft = detail.data?.draft;
    setScores(snapToStep(rubric.dims, draft?.scores ?? image.scores ?? {}));
    setDimComments(draft?.dim_comments ?? image.dim_comments ?? {});
    setDirty(false);
    setInitialized(image.id);
    startedAt.current = Date.now();
  }, [image, detail.data?.draft, initialized, rubric.dims]);

  const total = useMemo(() => computeTotal(rubric.dims, scores), [rubric.dims, scores]);
  const complete = rubric.dims.every((d) => scores[d.key] !== undefined);
  const totalValue = total ?? 0;
  const grade = useMemo(() => {
    const bands = [...rubric.grade_bands].sort((a, b) => b.min - a.min);
    return bands.find((b) => totalValue >= b.min)?.label ?? "";
  }, [rubric.grade_bands, totalValue]);

  const dimMin = rubric.dim_comment_min || 10;
  const dimMax = rubric.dim_comment_max || 50;
  const commentStates = dimCommentStates(rubric.dims, dimComments, dimMin, dimMax);
  const allCommentsOk = dimCommentsOk(rubric.dims, dimComments, dimMin, dimMax);
  // 提交按钮为什么点不动：把原因说清楚，而不是只把按钮变灰
  const missingDims = rubric.dims.filter((d) => scores[d.key] === undefined).map((d) => d.label);
  const blockingComment = firstBlockingDimComment(rubric.dims, dimComments, dimMin, dimMax);
  const submitBlockedReason = missingDims.length
    ? `还有 ${missingDims.length} 维没打分：${missingDims.join("、")}`
    : blockingComment
      ? `${blockingComment.label}点评${blockingComment.hint}`
      : "";

  const saveDraft = useCallback(
    (payload: Draft) => {
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
    [id, qc],
  );

  useEffect(() => {
    if (!dirty || !image || submitting.current) return;
    draftTimer.current = setTimeout(() => {
      saveDraft({ kind: "score", scores, dim_comments: dimComments }).catch((e) => {
        message.error(e instanceof ApiError ? e.message : "草稿保存失败，请重试");
      });
    }, 1000);
    return () => clearTimeout(draftTimer.current);
  }, [dirty, scores, dimComments, image, saveDraft]);

  const submit = useMutation({
    mutationFn: async (payload: Record<string, unknown>) => {
      clearTimeout(draftTimer.current);
      await draftSaves.current.catch(() => undefined);
      return api.post(`/images/${id}/submit`, { ...payload, client_submit_id: createSubmitId() });
    },
    onSuccess: () => {
      message.success("已提交，等待审核");
      qc.invalidateQueries({ queryKey: ["image", id] });
      qc.invalidateQueries({ queryKey: ["annotate-queue"] });
      qc.invalidateQueries({ queryKey: ["my-counts"] });
      qc.invalidateQueries({ queryKey: ["my-tasks"] });
      setDirty(false);
      const list = queue.data ?? [];
      const idx = list.findIndex((x) => x.id === id);
      const next = idx >= 0 ? list[idx + 1] : undefined;
      if (me?.limits.auto_next && next) nav(`/workbench/${next.id}`);
      else nav("/workbench");
    },
    onError: (e) => message.error(e instanceof Error ? e.message : "提交失败，请重试"),
    onSettled: () => { submitting.current = false; },
  });

  function send(payload: Record<string, unknown>) {
    if (submitting.current) return;
    submitting.current = true;
    submit.mutate(payload);
  }

  function doSubmit() {
    if (!complete) {
      message.warning(`五个维度都要打分才能提交（还差 ${rubric.dims.filter((d) => scores[d.key] === undefined).length} 个）`);
      return;
    }
    if (!allCommentsOk) {
      message.warning(blockingComment ? `${blockingComment.label}点评${blockingComment.hint}` : `每个维度都要写点评（${dimMin}~${dimMax} 字）`);
      return;
    }
    send({
      kind: "score",
      scores,
      dim_comments: dimComments,
      duration_ms: Date.now() - startedAt.current,
    });
  }

  // InputNumber commits its value on Enter in the same event as the shortcut.
  // Submit after React has applied that update, so we use the final score.
  useEffect(() => {
    if (!shortcutSubmit) return;
    setShortcutSubmit(false);
    doSubmit();
  });

  const list = queue.data ?? [];
  const idx = list.findIndex((x) => x.id === id);
  const prev = idx > 0 ? list[idx - 1] : undefined;
  const next = idx >= 0 && idx < list.length - 1 ? list[idx + 1] : undefined;

  function go(target?: ImageDTO) {
    if (!target || submitting.current) return;
    if (dirty) {
      const confirm = Modal.confirm({
        title: "当前标注还没提交",
        content: "切换前会把当前内容保存为草稿（仅你可见）。",
        okText: "保存草稿并切换",
        cancelText: "留在本页",
        onOk: (close) => {
          clearTimeout(draftTimer.current);
          confirm.update({ okButtonProps: { loading: true }, cancelButtonProps: { disabled: true } });
          saveDraft({ kind: "score", scores, dim_comments: dimComments }).then(() => {
            close();
            nav(`/workbench/${target.id}`);
          }).catch((e) => {
            message.error(e instanceof ApiError ? e.message : "草稿保存失败，请重试");
          }).finally(() => confirm.update({ okButtonProps: { loading: false }, cancelButtonProps: { disabled: false } }));
        },
      });
    } else {
      nav(`/workbench/${target.id}`);
    }
  }

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.defaultPrevented || excOpen || helpOpen || exampleOpen) return;
      const el = e.target as HTMLElement;
      const typing = ["INPUT", "TEXTAREA"].includes(el?.tagName);
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
        e.preventDefault();
        setShortcutSubmit(true);
        return;
      }
      if (typing) return;
      if (e.key === "ArrowLeft") go(prev);
      if (e.key === "ArrowRight") go(next);
      if (e.key === "?") setHelpOpen(true);
      if (e.key.toLowerCase() === "e") setExcOpen(true);
      // A/S/D/F/G：按维度顺序跳到分数框，直接键入 0~20
      const focusDim = (i: number) => {
        const d = rubric.dims[i];
        if (!d) return;
        setHotkeyDim(d.key);
        const el = document.querySelector<HTMLInputElement>(`[data-dim="${d.key}"] input`);
        el?.focus();
        el?.select();
      };
      const k = e.key.toLowerCase();
      if (k === "a") focusDim(0);
      if (k === "s") focusDim(1);
      if (k === "d") focusDim(2);
      if (k === "f") focusDim(3);
      if (k === "g") focusDim(4);
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
  if (detail.error || !image) {
    return <Empty description="图片不存在或已被回收" />;
  }

  const lastReject: Review | undefined = detail.data?.reviews.find((r) => r.action === "reject");

  return (
    <div className="annotate-shell">
      <div className="annotate-top">
        <Space size={14} wrap>
          <Tag color="blue">{image.folder}</Tag>
          <Typography.Text strong>{image.stored_name}</Typography.Text>
          <Typography.Text type="secondary">
            第 {idx >= 0 ? idx + 1 : 1} / {list.length || 1} 张
          </Typography.Text>
          {image.status === "rejected" && <Tag color="error">被打回 {image.reject_count} 次</Tag>}
        </Space>
        <Space>
          <Tooltip title="上一张 (←)">
            <Button aria-label="上一张" icon={<ArrowLeftOutlined />} disabled={!prev || submit.isPending} onClick={() => go(prev)} />
          </Tooltip>
          <Tooltip title="下一张 (→)">
            <Button aria-label="下一张" icon={<ArrowRightOutlined />} disabled={!next || submit.isPending} onClick={() => go(next)} />
          </Tooltip>
          <Button icon={<BookOutlined />} onClick={() => setExampleOpen(true)}>
            查看示例
          </Button>
          <Button icon={<QuestionCircleOutlined />} onClick={() => setHelpOpen(true)} />
        </Space>
      </div>

      <div className="annotate-body">
        <Card className="canvas-card" styles={{ body: { padding: 0, display: "flex", flex: 1 } }}>
          <ImageViewer src={image.original_url || image.preview_url} alt={image.stored_name} resetKey={image.id} />
        </Card>

        <div className="panel-card">
          <div className="panel-scroll">
          {lastReject && (
            <div className="reject-card">
              <div className="who">
                审核批注 · {lastReject.reviewer} ·{" "}
                {new Date(lastReject.created_at).toLocaleString("zh-CN")}
              </div>
              <div className="body">{lastReject.comment}</div>
              {lastReject.reason_tags?.length > 0 && (
                <Space size={4} wrap style={{ marginTop: 8 }}>
                  {lastReject.reason_tags.map((t) => (
                    <Tag key={t} color="red">
                      {t}
                    </Tag>
                  ))}
                </Space>
              )}
            </div>
          )}

          <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center", marginBottom: 10 }}>
            <Tag color="blue">{image.folder}</Tag>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              序号 {image.seq} · {image.status === "rejected" ? "已打回" : "标注中"}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12, maxWidth: 210, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={image.orig_name}>
              原图：{image.orig_name}
            </Typography.Text>
          </div>

          <ScorePanel
            dims={rubric.dims}
            value={scores}
            onPick={(dimKey, n) => {
              setScores((prevScores) => ({ ...prevScores, [dimKey]: n }));
              setDirty(true);
            }}
            hotkeyDim={hotkeyDim}
          />

          <div className="total-box" style={{ marginTop: 14 }}>
            <span className="total-num">{complete ? totalValue.toFixed(1) : "--"}</span>
            <span className="total-grade">
              {complete ? <Tag color={totalValue >= 90 ? "green" : totalValue >= 60 ? "blue" : "volcano"}>{grade}</Tag> : `${rubric.dims.length} 维打分后自动计算`}
            </span>
            <span style={{ marginLeft: "auto", fontSize: 12, color: "#7b8598" }}>权重：{rubric.name}</span>
          </div>

          <div style={{ marginTop: 14 }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
              <b>点评（每个维度各写一条）</b>
              <Space size={8}>
                <span style={{ fontSize: 12, color: "#7b8598" }}>{dimMin}~{dimMax} 字/维</span>
              </Space>
            </div>
            <div className="dim-comment-list">
              {commentStates.map((s) => (
                <div className="dim-comment-row" key={s.key} data-dim-comment={s.key}>
                  <div style={{ width: 100, fontSize: 13, color: "#44506a", flex: "0 0 auto" }}>
                    <div>{s.label} · {scores[s.key] ?? "—"}/{rubric.dims.find((d) => d.key === s.key)?.max}</div>
                    <Dropdown
                      menu={{
                        items: (me?.comment_templates ?? []).map((text, i) => ({ key: String(i), label: text })),
                        onClick: ({ key }) => {
                          const text = me?.comment_templates?.[Number(key)];
                          if (!text) return;
                          setDimComments((prev) => {
                            const current = prev[s.key] ?? "";
                            return { ...prev, [s.key]: current ? current.replace(/。?$/, "。") + text : text };
                          });
                          setDirty(true);
                        },
                      }}
                    >
                      <Button size="small" type="link" aria-label={`${s.label}常用评语`}>常用评语 ▾</Button>
                    </Dropdown>
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <Input.TextArea
                      rows={1}
                      maxLength={dimMax}
                      value={dimComments[s.key] ?? ""}
                      placeholder={`写清这一维的问题，${dimMin}~${dimMax} 字`}
                      data-test={`dim-comment-${s.key}`}
                      onChange={(e) => {
                        const v = e.target.value;
                        setDimComments((prev) => ({ ...prev, [s.key]: v }));
                        setDirty(true);
                      }}
                    />
                    <div
                      data-test={`dim-comment-hint-${s.key}`}
                      style={{ fontSize: 11, marginTop: 2, color: s.ok ? "#7b8598" : "#cf1322" }}
                    >
                      {s.hint}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          </div>

          <div className="panel-footer" style={{ display: "flex", gap: 8 }}>
            <Tooltip title={submitBlockedReason || "提交并自动进入下一张（⌘/Ctrl + Enter）"}>
              <span style={{ flex: 1, display: "inline-flex" }}>
                <Button
                  type="primary"
                  size="large"
                  style={{ width: "100%" }}
                  icon={<SendOutlined />}
                  loading={submit.isPending}
                  onClick={doSubmit}
                  data-test="submit"
                >
                  提交并下一张
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="这张图不能打分时使用（E）">
              <Button
                danger
                size="large"
                icon={<ExclamationCircleOutlined />}
                onClick={() => setExcOpen(true)}
                data-test="exception"
              >
                异常
              </Button>
            </Tooltip>
          </div>
        </div>
      </div>

      <ExceptionPanel
        open={excOpen}
        reasons={me?.exception_reasons ?? []}
        loading={submit.isPending}
        onCancel={() => setExcOpen(false)}
        onSubmit={(reasons, note) =>
          send({
            kind: "exception",
            exception_reasons: reasons,
            exception_note: note,
            duration_ms: Date.now() - startedAt.current,
          })
        }
      />

      <ExampleDrawer open={exampleOpen} onClose={() => setExampleOpen(false)} />

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
