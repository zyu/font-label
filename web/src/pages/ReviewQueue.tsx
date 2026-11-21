import MediaImage from "../components/MediaImage";
import { useAuth } from "../auth";
import RatingSummary from "../components/RatingSummary";
import ListTable from "../components/ListTable";
import { Badge, Button, Card, Empty, Segmented, Select, Space, Tag, Typography } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { STATUS_LABEL, type FolderDTO, type ReviewQueueItem, type Page } from "../api/types";

export default function ReviewQueuePage() {
  const { me } = useAuth();
  const nav = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "pending";
  const kind = params.get("kind") ?? "";
  const folderId = Number(params.get("folder_id")) || undefined;
  const page = Math.max(1, Number(params.get("page")) || 1);
  const setFilter = (key: string, value: string) => setParams((current) => {
    const next = new URLSearchParams(current);
    if (key !== "page") next.delete("page");
    if (value) next.set(key, value); else next.delete(key);
    return next;
  });
  const openReview = (row: ReviewQueueItem) => {
    const snapshot = row.can_review === false && row.review_id ? `?review_id=${row.review_id}` : "";
    nav(`/review/${row.id}${snapshot}`, { state: { returnTo: `${location.pathname}${location.search}` } });
  };
  function changeTab(value: string) {
    const next = new URLSearchParams(params);
    next.delete("page");
    next.delete("kind");
    next.delete("status");
    if (value === "score" || value === "exception") next.set("kind", value);
    else if (value === "reviewed" || value === "all") next.set("status", value);
    setParams(next);
  }

  const folders = useQuery({
    queryKey: ["folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders"),
  });

  const queue = useQuery({
    queryKey: ["review-queue", kind, folderId, page, status],
    queryFn: () => {
      const qs = new URLSearchParams({ page_size: "20", page: String(page) });
      if (status !== "pending") qs.set("status", status);
      if (kind) qs.set("kind", kind);
      if (folderId) qs.set("folder_id", String(folderId));
      return api.get<Page<ReviewQueueItem>>(`/reviews/queue?${qs.toString()}`);
    },
  });

  const items = queue.data?.items ?? [];
  const first = items.find((item) => item.can_review ?? item.status === "submitted");

  const columns = [
    {
      title: "图片",
      width: 80,
      render: (_: unknown, row: ReviewQueueItem) => (
        <MediaImage className="thumb" src={row.thumb_url} alt={row.stored_name} onClick={() => openReview(row)} />
      ),
    },
    { title: "文件名", dataIndex: "stored_name", render: (v: string, row: ReviewQueueItem) => <a onClick={() => openReview(row)}>{v}</a> },
    { title: "文件夹", dataIndex: "folder" },
    { title: "分组", dataIndex: "group", render: (group?: string) => group || "—" },
    { title: "标注员", dataIndex: "annotator" },
    {
      title: "类型",
      width: 100,
      render: (_: unknown, row: ReviewQueueItem) =>
        row.kind === "exception" ? <Tag color="orange">照片不合格</Tag> : <Tag color="blue">评级</Tag>,
    },
    {
      title: "五维等级",
      width: 110,
      render: (_: unknown, row: ReviewQueueItem) => <RatingSummary dimensions={me?.rubric.dims ?? []} grades={row.grades} />,
    },
    {
      title: "提交时间",
      dataIndex: "submitted_at",
      width: 170,
      render: (v: string) => (v ? new Date(v).toLocaleString("zh-CN") : "-"),
    },
    {
      title: "审核结果",
      width: 130,
      render: (_: unknown, row: ReviewQueueItem) => row.can_review ?? row.status === "submitted"
        ? <Tag color="orange">待审核</Tag>
        : <Tag>{STATUS_LABEL[row.review_result ?? row.status]}</Tag>,
    },
    {
      title: "审核时间",
      width: 170,
      dataIndex: "reviewed_at",
      render: (value?: string) => value ? new Date(value).toLocaleString("zh-CN") : "—",
    },
    {
      title: "操作",
      width: 140,
      render: (_: unknown, row: ReviewQueueItem) => (
        <Button type="primary" size="small" onClick={() => openReview(row)}>
          {(row.can_review ?? row.status === "submitted") ? "审核" : "查看"}
        </Button>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Card size="small">
        <Space wrap size={16}>
          <Space>
            <Typography.Text type="secondary">类型</Typography.Text>
            <Segmented
              value={status === "pending" ? kind || "pending" : status}
              onChange={changeTab}
              options={[
                { label: "待审核", value: "pending" },
                { label: "待审核 · 评级", value: "score" },
                { label: "待审核 · 照片不合格", value: "exception" },
                { label: "已审核", value: "reviewed" },
                { label: "全部", value: "all" },
              ]}
            />
          </Space>
          <Space>
            <Typography.Text type="secondary">文件夹</Typography.Text>
            <Select
              allowClear
              placeholder="全部文件夹"
              style={{ minWidth: 200 }}
              value={folderId}
              onChange={(value) => setFilter("folder_id", value ? String(value) : "")}
              options={(folders.data?.items ?? []).map((f) => ({ label: `${f.name}（待审 ${f.submitted}）`, value: f.id }))}
            />
          </Space>
          <Badge count={queue.data?.total ?? 0} showZero color="#2b5cff" />
          <Button type="primary" disabled={!first} onClick={() => first && openReview(first)}>
            开始逐条审核
          </Button>
        </Space>
      </Card>

      <Card size="small" title={`${status === "reviewed" ? "已审核" : status === "all" ? "全部审核任务" : "待审核队列"}（${queue.data?.total ?? 0}）`}>
        <ListTable
          rowKey="id"
          size="middle"
          loading={queue.isLoading}
          dataSource={items}
          columns={columns as never}
          pagination={{ current: page, total: queue.data?.total, pageSize: 20, showSizeChanger: false, onChange: (value) => setFilter("page", value > 1 ? String(value) : "") }}
          locale={{ emptyText: <Empty description={status === "pending" ? "当前没有待审核任务" : "当前没有符合条件的记录"} /> }}
        />
      </Card>

      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        说明：审核员按提交时间处理同组任务。打回时填写的批注会显示给标注员。
        <span style={{ display: "none" }}>{STATUS_LABEL.submitted}</span>
      </Typography.Text>
    </Space>
  );
}
