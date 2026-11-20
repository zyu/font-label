import { useState } from "react";
import { Alert, Card, Input, Space, Table, Typography } from "antd";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Page } from "../../api/types";

interface AuditEntry {
  id: number;
  actor: string;
  username: string;
  action: string;
  target_type: string;
  target_id: number;
  ip: string;
  detail: Record<string, unknown>;
  created_at: string;
}

export default function AuditLogs() {
  const [page, setPage] = useState(1);
  const [before, setBefore] = useState(0);
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const entries = useQuery({
    queryKey: ["audit-logs", page, before, action, actor],
    queryFn: () => api.get<Page<AuditEntry> & { before: number }>(
      "/audit-logs?" + new URLSearchParams({ page: String(page), page_size: "30", before: String(before), action, actor }),
    ),
  });
  const reset = () => { setPage(1); setBefore(0); };

  return (
    <Card title="操作日志">
      <Space style={{ marginBottom: 16 }}>
        <Input.Search allowClear placeholder="操作，例如 group.dispatch" onSearch={(value) => { setAction(value.trim()); reset(); }} />
        <Input.Search allowClear placeholder="姓名或账号" onSearch={(value) => { setActor(value.trim()); reset(); }} />
      </Space>
      {entries.error && <Alert type="error" message={entries.error.message} />}
      <Table<AuditEntry> rowKey="id" size="small" loading={entries.isFetching} dataSource={entries.data?.items ?? []}
        pagination={{
          current: page, pageSize: 30, total: entries.data?.total, showSizeChanger: false,
          onChange: (value) => { setBefore(entries.data?.before ?? 0); setPage(value); },
        }}
        expandable={{ expandedRowRender: (row) => (
          <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", margin: 0 }}>{JSON.stringify(row.detail, null, 2)}</pre>
        ) }}
        columns={[
          { title: "时间", dataIndex: "created_at", render: (value: string) => new Date(value).toLocaleString("zh-CN") },
          { title: "操作人", render: (_, row) => row.username ? `${row.actor}（${row.username}）` : row.actor },
          { title: "操作", dataIndex: "action" },
          { title: "对象", render: (_, row) => <Typography.Text code>{row.target_type}{row.target_id ? ` #${row.target_id}` : ""}</Typography.Text> },
          { title: "请求", render: (_, row) => typeof row.detail?.path === "string" ? `${row.detail.path} · ${row.detail.status ?? ""}` : "—" },
          { title: "IP", dataIndex: "ip" },
        ]}
      />
    </Card>
  );
}
