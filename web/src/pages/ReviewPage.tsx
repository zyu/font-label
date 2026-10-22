import { useEffect, useState } from "react";
import { Alert, Button, Card, Descriptions, Empty, Input, Modal, Space, Spin, Tag, Typography, message } from "antd";
import { CheckOutlined, CloseOutlined, ArrowLeftOutlined, ArrowRightOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../api/client";
import type { ImageDTO, ImageDetail, Page } from "../api/types";
import { useAuth } from "../auth";
import ImageViewer from "../components/ImageViewer";

export default function ReviewPage() {
  const { imageId } = useParams();
  const id = Number(imageId);
  const nav = useNavigate();
  const qc = useQueryClient();
  const { me } = useAuth();
  const [rejectOpen, setRejectOpen] = useState(false);
  const [comment, setComment] = useState("");
  const [tags, setTags] = useState<string[]>([]);

  const detail = useQuery({
    queryKey: ["image", id],
    queryFn: () => api.get<ImageDetail>(`/images/${id}`),
    enabled: id > 0,
  });

  const queue = useQuery({
    queryKey: ["review-queue-nav"],
    queryFn: () => api.get<Page<ImageDTO>>("/reviews/queue?page_size=200"),
  });

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
      const next = idx >= 0 ? list[idx + 1] : undefined;
      nav(next ? `/review/${next.id}` : "/review");
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "操作失败"),
  });

  // 切换到另一张图时，未提交的批注状态不应残留
  useEffect(() => {
    setRejectOpen(false);
    setComment("");
    setTags([]);
  }, [id]);

  if (detail.isLoading) {
    return (
      <div style={{ padding: 80, textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }
  const image = detail.data?.image;
  if (!image) return <Empty description="图片不存在" />;
  const list = queue.data?.items ?? [];
  const idx = list.findIndex((x) => x.id === id);
  const prev = idx > 0 ? list[idx - 1] : undefined;
  const next = idx >= 0 ? list[idx + 1] : undefined;

  const dims = me!.rubric.dims;
  const latest = detail.data?.reviews?.[0];

  return (
    <div className="annotate-shell">
      <div className="annotate-top">
        <Space size={14} wrap>
          <Tag color="blue">{image.folder}</Tag>
          <Typography.Text strong>{image.stored_name}</Typography.Text>
          <Typography.Text type="secondary">标注员：{image.annotator || "-"}</Typography.Text>
          <Typography.Text type="secondary">
            队列 {idx >= 0 ? idx + 1 : 1} / {list.length || 1}
          </Typography.Text>
          {image.reject_count > 0 && <Tag color="volcano">历史驳回 {image.reject_count} 次</Tag>}
        </Space>
        <Space>
          <Button icon={<ArrowLeftOutlined />} disabled={!prev} onClick={() => nav(`/review/${prev!.id}`)} />
          <Button icon={<ArrowRightOutlined />} disabled={!next} onClick={() => nav(`/review/${next!.id}`)} />
        </Space>
      </div>

      <div className="annotate-body">
        <Card className="canvas-card" styles={{ body: { padding: 0, display: "flex", flex: 1 } }}>
          <ImageViewer src={image.preview_url} alt={image.stored_name} resetKey={image.id} />
        </Card>

        <div className="panel-card">
          <Descriptions
            size="small"
            column={2}
            title="标注内容"
            items={[
              { key: "kind", label: "类型", children: image.kind === "exception" ? <Tag color="orange">异常上报</Tag> : <Tag color="blue">评分</Tag> },
              {
                key: "total",
                label: "总分",
                children: image.total === null ? "-" : <b>{image.total} {image.grade}</b>,
              },
              ...dims.map((d) => ({
                key: d.key,
                label: d.label,
                children: image.scores?.[d.key] ?? "-",
              })),
            ]}
          />

          {image.kind === "exception" ? (
            <Alert
              type="warning"
              showIcon
              style={{ margin: "12px 0" }}
              title="这张图被标注为不可评（异常）"
              description={
                <Space direction="vertical" size={2}>
                  {(image.exception_reasons ?? []).map((r) => (
                    <span key={r}>
                      · {me?.exception_reasons.find((x) => x.key === r)?.label ?? r}
                    </span>
                  ))}
                  {image.comment && <span>备注：{image.comment}</span>}
                </Space>
              }
            />
          ) : (
            <Card size="small" title="点评" style={{ margin: "12px 0" }} styles={{ body: { whiteSpace: "pre-wrap" } }}>
              {image.comment || <span style={{ color: "#999" }}>（空）</span>}
            </Card>
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

          <Space direction="vertical" style={{ width: "100%" }} size={10}>
            <Button
              type="primary"
              size="large"
              block
              icon={<CheckOutlined />}
              loading={review.isPending}
              disabled={image.status !== "submitted"}
              onClick={() => review.mutate({ action: "approve" })}
              data-test="approve"
            >
              通过（{image.kind === "exception" ? "异常成立，记无效图" : "评分合格"}）
            </Button>
            <Button
              danger
              size="large"
              block
              icon={<CloseOutlined />}
              disabled={image.status !== "submitted"}
              onClick={() => {
                setComment("");
                setTags([]);
                setRejectOpen(true);
              }}
              data-test="reject"
            >
              批注打回
            </Button>
            {image.status !== "submitted" && (
              <Typography.Text type="secondary">该图片当前状态为「{image.status}」，不能再次审核。</Typography.Text>
            )}
          </Space>
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
          {(me?.reject_reason_tags ?? []).map((t) => (
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
          placeholder="指出具体问题，例如：撇画太短没送到左下角，参差分应更低"
          data-test="reject-comment"
        />
      </Modal>
    </div>
  );
}
