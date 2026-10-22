import { useState } from "react";
import { Badge, Button, Card, Empty, Segmented, Select, Space, Table, Tag, Typography } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { STATUS_LABEL, type FolderDTO, type ImageDTO, type Page } from "../api/types";

export default function ReviewQueuePage() {
  const nav = useNavigate();
  const [kind, setKind] = useState<string>("");
  const [folderId, setFolderId] = useState<number | undefined>();

  const folders = useQuery({
    queryKey: ["folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders"),
  });

  const queue = useQuery({
    queryKey: ["review-queue", kind, folderId],
    queryFn: () => {
      const qs = new URLSearchParams({ page_size: "100" });
      if (kind) qs.set("kind", kind);
      if (folderId) qs.set("folder_id", String(folderId));
      return api.get<Page<ImageDTO>>(`/reviews/queue?${qs.toString()}`);
    },
  });

  const items = queue.data?.items ?? [];
  const first = items[0];

  const columns = [
    {
      title: "图片",
      width: 80,
      render: (_: unknown, row: ImageDTO) => (
        <img className="thumb" src={row.thumb_url} alt={row.stored_name} onClick={() => nav(`/review/${row.id}`)} />
      ),
    },
    { title: "文件名", dataIndex: "stored_name", render: (v: string, row: ImageDTO) => <a onClick={() => nav(`/review/${row.id}`)}>{v}</a> },
    { title: "文件夹", dataIndex: "folder" },
    { title: "标注员", dataIndex: "annotator" },
    {
      title: "类型",
      width: 100,
      render: (_: unknown, row: ImageDTO) =>
        row.kind === "exception" ? <Tag color="orange">异常上报</Tag> : <Tag color="blue">评分</Tag>,
    },
    {
      title: "总分",
      width: 110,
      render: (_: unknown, row: ImageDTO) => (row.total === null ? "-" : `${row.total} ${row.grade}`),
    },
    {
      title: "提交时间",
      dataIndex: "submitted_at",
      width: 170,
      render: (v: string) => (v ? new Date(v).toLocaleString("zh-CN") : "-"),
    },
    {
      title: "操作",
      width: 140,
      render: (_: unknown, row: ImageDTO) => (
        <Button type="primary" size="small" onClick={() => nav(`/review/${row.id}`)}>
          审核
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
              value={kind}
              onChange={(v) => setKind(String(v))}
              options={[
                { label: "全部", value: "" },
                { label: "评分", value: "score" },
                { label: "异常", value: "exception" },
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
              onChange={setFolderId}
              options={(folders.data?.items ?? []).map((f) => ({ label: `${f.name}（待审 ${f.submitted}）`, value: f.id }))}
            />
          </Space>
          <Badge count={queue.data?.total ?? 0} showZero color="#2b5cff" />
          <Button type="primary" disabled={!first} onClick={() => first && nav(`/review/${first.id}`)}>
            开始逐条审核
          </Button>
        </Space>
      </Card>

      <Card size="small" title={`待审核队列（${queue.data?.total ?? 0}）`}>
        <Table
          rowKey="id"
          size="middle"
          loading={queue.isLoading}
          dataSource={items}
          columns={columns as never}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="队列空了，所有提交都审完了" /> }}
        />
      </Card>

      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        说明：审核队列只显示别人标注的图片（不会出现自己的），按提交时间先进先出。打回必须填写批注，标注员会看到原文。
        <span style={{ display: "none" }}>{STATUS_LABEL.submitted}</span>
      </Typography.Text>
    </Space>
  );
}
