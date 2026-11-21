import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Card, Descriptions, Empty, Input, Select, Modal, Space, Spin, Tag, Typography, message } from "antd";
import { CheckOutlined, CloseOutlined, ArrowLeftOutlined, ArrowRightOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../api/client";
import type { ReviewQueueItem, ImageDetail, Page } from "../api/types";
import { exceptionReasonLabel, stringSetting } from "../lib/settings";
import { useAuth } from "../auth";
import ImageViewer from "../components/ImageViewer";
import { LEVELS, type Level } from "../lib/annotationLevels";

export default function ReviewPage() {
  const { imageId } = useParams();
  const id = Number(imageId);
  const nav = useNavigate();
  const location = useLocation();
  const returnTo = location.state?.returnTo?.startsWith("/review?") ? location.state.returnTo : "/review";
  const reviewId = new URLSearchParams(location.search).get("review_id");
  const listParams = new URLSearchParams(returnTo.split("?")[1]);
  listParams.set("page_size", "20");
  const listURL = `/reviews/queue?${listParams}`;
  const navigateToImage = (item: ReviewQueueItem) => {
    const snapshot = item.can_review === false && item.review_id ? `?review_id=${item.review_id}` : "";
    nav(`/review/${item.id}${snapshot}`, { state: location.state });
  };
  const qc = useQueryClient();
  const { me } = useAuth();
  const rubric = me!.rubric;
  const dims = rubric.dims;
  const [rejectOpen, setRejectOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [patched, setPatched] = useState<Record<string, Level>>({});

  const detail = useQuery({
    queryKey: reviewId ? ["image", id, "review", reviewId] : ["image", id],
    queryFn: () => api.get<ImageDetail>(`/images/${id}${reviewId ? `?review_id=${encodeURIComponent(reviewId)}` : ""}`),
    enabled: id > 0,
  });

  const queue = useQuery({
    queryKey: ["review-queue-nav", listURL],
    queryFn: () => api.get<Page<ReviewQueueItem>>(listURL),
  });

  const image = detail.data?.image;

  const review = useMutation({
    mutationFn: (body: unknown) => api.post(`/images/${id}/review`, body),
    onSuccess: () => {
      message.success("已处理");
      setRejectOpen(false);
      setComment("");
      setTags([]);
      qc.invalidateQueries({ queryKey: ["image", id] });
      qc.invalidateQueries({ queryKey: ["review-queue"] });
      qc.invalidateQueries({ queryKey: ["review-queue-nav"] });
      const list = queue.data?.items ?? [];
      const idx = list.findIndex((x) => x.id === id);
      void qc.invalidateQueries({ queryKey: ["review-stats"] });
      const next = idx >= 0 ? list.slice(idx + 1).find((item) => item.can_review ?? item.status === "submitted") : undefined;
      if (next) navigateToImage(next); else nav(returnTo);
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "操作失败"),
  });

  // 切换到另一张图时，未提交的批注状态不应残留
  useEffect(() => {
    setRejectOpen(false);
    setComment("");
    setTags([]);
  }, [id, reviewId]);

  // 切图时恢复该图的原始等级。
  useEffect(() => {
    if (!image) return;
    setPatched({ ...(image.grades ?? {}) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [image?.id, reviewId]);

  const mergedGrades = useMemo(
    () => ({ ...(image?.grades ?? {}), ...patched }),
    [image?.grades, patched],
  );

  // 仅提交变化的维度。
  const changedGradePatch = useMemo(() => {
    const out: Record<string, Level> = {};
    if (image?.kind !== "score") return out;
    for (const d of dims) {
      const cur = mergedGrades[d.key];
      if (cur !== undefined && cur !== image.grades?.[d.key]) out[d.key] = cur;
    }
    return out;
  }, [dims, mergedGrades, image]);

  const changedCount = Object.keys(changedGradePatch).length;

  if (detail.isLoading) {
    return (
      <div style={{ padding: 80, textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }
  if (!image) return <Empty description="图片不存在" />;
  const list = queue.data?.items ?? [];
  const idx = list.findIndex((x) => x.id === id);
  const prev = idx > 0 ? list[idx - 1] : undefined;
  const next = idx >= 0 ? list[idx + 1] : undefined;

  const latest = detail.data?.reviews?.[0];
  const isException = image.kind === "exception";
  const canReview = !reviewId && image.status === "submitted";

  return (
    <div className="annotate-shell">
      <div className="annotate-top">
        <Space size={14} wrap>
          <Button aria-label="返回列表" icon={<ArrowLeftOutlined />} disabled={review.isPending} onClick={() => nav(returnTo)}>返回列表</Button>
          <Tag color="blue">{image.folder}</Tag>
          <Typography.Text strong>{image.stored_name}</Typography.Text>
          <Typography.Text type="secondary">标注员：{image.annotator || "-"}</Typography.Text>
          <Typography.Text type="secondary">
            队列 {idx >= 0 ? idx + 1 : 1} / {list.length || 1}
          </Typography.Text>
          {image.reject_count > 0 && <Tag color="volcano">历史驳回 {image.reject_count} 次</Tag>}
        </Space>
        <Space>
          <Button aria-label="上一张" icon={<ArrowLeftOutlined />} disabled={!prev || review.isPending} onClick={() => navigateToImage(prev!)} />
          <Button aria-label="下一张" icon={<ArrowRightOutlined />} disabled={!next || review.isPending} onClick={() => navigateToImage(next!)} />
        </Space>
      </div>

      <div className="annotate-body">
        <Card className="canvas-card" styles={{ body: { padding: 0, display: "flex", flex: 1 } }}>
          <ImageViewer src={image.original_url || image.preview_url} alt={image.stored_name} resetKey={image.id} />
        </Card>

        <div className="panel-card">
          <div className="panel-scroll">
          {!isException && <Descriptions
            size="small"
            column={2}
            title="标注内容"
            items={[
              ...dims.map((d) => ({
                key: d.key,
                label: d.label,
                children: (
                  <div>
                    <b>{image.grades?.[d.key] ?? "-"}</b>
                    <div style={{ fontSize: 12, color: image.dim_comments?.[d.key] ? "#44506a" : "#bbb", marginTop: 2, whiteSpace: "pre-wrap" }}>
                      {image.dim_comments?.[d.key] || "（该维未写点评）"}
                    </div>
                  </div>
                ),
              })),
            ]}
          />}

          {image.kind === "score" && canReview && (
            <Card
              size="small"
              title="等级修正（可选）"
              style={{ margin: "12px 0" }}
              extra={changedCount > 0 ? `修正 ${changedCount} 维` : undefined}
            >
              <Space direction="vertical" style={{ width: "100%" }} size={6}>
                {dims.map((d) => {
                  const cur = mergedGrades[d.key];
                  const orig = image.grades?.[d.key];
                  const changed = cur !== undefined && cur !== orig;
                  return (
                    <div
                      key={d.key}
                      data-review-dim={d.key}
                      style={{ display: "flex", alignItems: "center", gap: 8 }}
                    >
                      <span style={{ width: 92, fontSize: 13 }}>{d.label}</span>
                      <Select<Level>
                        size="small"
                        value={cur}
                        options={LEVELS.map((value) => ({ label: value, value }))}
                        disabled={!canReview}
                        onChange={(v) =>
                          setPatched((p) => ({ ...p, [d.key]: v }))
                        }
                        data-test={`review-grade-${d.key}`}
                        style={{ width: 66 }}
                      />
                      {changed && <Tag color="orange">原 {orig ?? "—"}</Tag>}
                    </div>
                  );
                })}
              </Space>
              <div style={{ fontSize: 12, color: "#7b8598", marginTop: 6 }}>
                点击「通过」保存修正后的维度等级。
              </div>
            </Card>
          )}

          {isException && (
            <Alert
              type="warning"
              showIcon
              style={{ margin: "12px 0" }}
              title="照片不合格原因"
              description={
                <Space direction="vertical" size={2}>
                  {(image.exception_reasons ?? []).map((r) => (
                    <span key={r}>
                      · {exceptionReasonLabel(r, me?.exception_reasons)}
                    </span>
                  ))}
                  {image.comment && <span>备注：{image.comment}</span>}
                  {image.status === "submitted" && <span>确认后，这张照片会计入无效图。</span>}
                </Space>
              }
            />
          )}

          {latest && (
            <Card size="small" title="上一次审核" style={{ marginBottom: 12 }} styles={{ body: { whiteSpace: "pre-wrap" } }}>
              <Space size={6} wrap style={{ marginBottom: 4 }}>
                <Tag color={latest.action === "approve" ? "green" : "red"}>
                  {latest.action === "approve" ? "通过" : "打回"}
                </Tag>
                <span>{latest.reviewer}</span>
                <span style={{ color: "#999" }}>{new Date(latest.created_at).toLocaleString("zh-CN")}</span>
              </Space>
              <div>{latest.comment}</div>
            </Card>
          )}

          {!canReview && <Typography.Text type="secondary">{reviewId ? "已审核记录" : "该图片当前为只读"}</Typography.Text>}
          </div>
          {canReview && <div className="panel-footer review-footer">
            <Button
              type="primary"
              size="large"
              icon={<CheckOutlined aria-hidden="true" />}
              loading={review.isPending}
              disabled={!canReview}
              onClick={() =>
                review.mutate({
                  action: "approve",
                  ...(changedCount > 0 ? { grade_patch: changedGradePatch } : {}),
                  ...(comment.trim() ? { comment: comment.trim() } : {}),
                })
              }
              data-test="approve"
            >
              {isException ? "确认照片不合格" : "通过（等级合格）"}
            </Button>
            <Button
              danger
              size="large"
              icon={<CloseOutlined />}
              disabled={!canReview}
              onClick={() => {
                setComment("");
                setTags([]);
                setRejectOpen(true);
              }}
              data-test="reject"
            >
              批注打回
            </Button>
          </div>}
        </div>
      </div>

      <Modal
        open={rejectOpen}
        title="打回并填写批注（标注员会看到）"
        okText="确认打回"
        okButtonProps={{ danger: true, loading: review.isPending }}
        onCancel={() => setRejectOpen(false)}
        onOk={() => {
          if ([...comment.trim()].length < 5) {
            message.warning("批注至少 5 个字");
            return;
          }
          review.mutate({ action: "reject", comment, reason_tags: tags });
        }}
        destroyOnHidden
      >
        <Space size={6} wrap style={{ marginBottom: 10 }}>
          {stringSetting(me?.reject_reason_tags).map((t) => (
            <Tag.CheckableTag
              key={t}
              checked={tags.includes(t)}
              onChange={(checked) => setTags((prevTags) => (checked ? [...prevTags, t] : prevTags.filter((x) => x !== t)))}
            >
              {t}
            </Tag.CheckableTag>
          ))}
        </Space>
        <Input.TextArea
          rows={4}
          maxLength={300}
          showCount
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder="指出具体问题，例如：撇画太短没送到左下角，参差等级应更低"
          data-test="reject-comment"
        />
      </Modal>
    </div>
  );
}
