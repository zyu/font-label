import { Card, Col, Empty, Progress, Row, Space, Statistic, Table, Tag, Typography } from "antd";
import { useQuery } from "@tanstack/react-query";
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip as ReTooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api } from "../api/client";
import type { FolderDTO } from "../api/types";
import { useAuth } from "../auth";

interface Overview {
  total: number;
  unassigned: number;
  assigned: number;
  submitted: number;
  approved: number;
  rejected: number;
  excluded: number;
  today_upload: number;
  today_submit: number;
  today_review: number;
  today_reject: number;
  avg_total: number;
  reject_rate: number;
}

interface AnnotatorRow {
  id: number;
  name: string;
  status: string;
  granted: number;
  submitted: number;
  approved: number;
  rejected: number;
  pending: number;
  holding: number;
  avg_total: number;
  avg_ms: number;
  exceptions: number;
  reject_rate: number;
  first_pass_rate: number;
  reject_events: number;
}

export default function StatsPage() {
  const { user } = useAuth();
  const isAnnotator = user?.role === "annotator";

  const overview = useQuery({
    queryKey: ["stats-overview"],
    queryFn: () => api.get<Overview>("/stats/overview"),
    enabled: !isAnnotator,
  });
  const annotators = useQuery({
    queryKey: ["stats-annotators"],
    queryFn: () => api.get<{ items: AnnotatorRow[] }>("/stats/annotators"),
    enabled: !isAnnotator,
  });
  const folders = useQuery({
    queryKey: ["stats-folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/stats/folders"),
    enabled: !isAnnotator,
  });
  const rejections = useQuery({
    queryKey: ["stats-rejections"],
    queryFn: () => api.get<{ reasons: { tag: string; count: number }[]; by_annotator: { name: string; count: number }[] }>("/stats/rejections"),
    enabled: !isAnnotator,
  });
  const series = useQuery({
    queryKey: ["stats-series"],
    queryFn: () => api.get<{ points: { date: string; submitted: number; approved: number; rejected: number }[] }>("/stats/timeseries?days=14"),
    enabled: !isAnnotator,
  });
  const mine = useQuery({
    queryKey: ["stats-me"],
    queryFn: () =>
      api.get<{
        holding: number;
        rejected: number;
        approved: number;
        pending: number;
        submitted_today: number;
        granted: number;
        avg_total: number;
        reject_events: number;
      }>("/stats/me"),
    enabled: isAnnotator,
  });

  if (isAnnotator) {
    const m = mine.data;
    return (
      <Space direction="vertical" size={14} style={{ width: "100%" }}>
        <Row gutter={14}>
          {[
            ["待完成", m?.holding ?? 0, "#2b5cff"],
            ["被驳回", m?.rejected ?? 0, "#cf1322"],
            ["待审核", m?.pending ?? 0, "#fa8c16"],
            ["已通过", m?.approved ?? 0, "#389e0d"],
            ["今日提交", m?.submitted_today ?? 0, "#722ed1"],
            ["平均分", Number(m?.avg_total ?? 0).toFixed(1), "#13c2c2"],
          ].map(([label, value, color]) => (
            <Col span={4} key={String(label)}>
              <div className="stat-card">
                <div className="stat-num" style={{ color: String(color) }}>
                  {value}
                </div>
                <div className="stat-label">{label}</div>
              </div>
            </Col>
          ))}
        </Row>
        <Card size="small" title="我的情况">
          <Typography.Paragraph>
            累计领取 <b>{m?.granted ?? 0}</b> 张；被驳回 <b>{m?.reject_events ?? 0}</b> 次。
            驳回后请到「标注工作台 · 已驳回」按审核批注修改并重新提交。
          </Typography.Paragraph>
        </Card>
      </Space>
    );
  }

  const o = overview.data;
  const cards: [string, number | string, string][] = [
    ["总图片", o?.total ?? 0, "#1c2333"],
    ["待领取", o?.unassigned ?? 0, "#8c8c8c"],
    ["进行中", o?.assigned ?? 0, "#2b5cff"],
    ["待审核", o?.submitted ?? 0, "#fa8c16"],
    ["已通过", o?.approved ?? 0, "#389e0d"],
    ["已打回", o?.rejected ?? 0, "#cf1322"],
    ["无效图", o?.excluded ?? 0, "#595959"],
    ["驳回率", `${Number(o?.reject_rate ?? 0).toFixed(1)}%`, "#cf1322"],
    ["平均总分", Number(o?.avg_total ?? 0).toFixed(1), "#13c2c2"],
    ["今日提交", o?.today_submit ?? 0, "#722ed1"],
    ["今日审核", o?.today_review ?? 0, "#722ed1"],
    ["今日上传", o?.today_upload ?? 0, "#722ed1"],
  ];

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Row gutter={[12, 12]}>
        {cards.map(([label, value, color]) => (
          <Col span={4} key={label}>
            <div className="stat-card">
              <div className="stat-num" style={{ color }}>
                {value}
              </div>
              <div className="stat-label">{label}</div>
            </div>
          </Col>
        ))}
      </Row>

      <Row gutter={14}>
        <Col span={15}>
          <Card size="small" title="近 14 天：提交 / 通过 / 打回">
            <div style={{ height: 260 }}>
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={series.data?.points ?? []}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#eef0f4" />
                  <XAxis dataKey="date" tick={{ fontSize: 11 }} tickFormatter={(v: string) => v.slice(5)} />
                  <YAxis tick={{ fontSize: 11 }} allowDecimals={false} />
                  <ReTooltip />
                  <Legend />
                  <Line type="monotone" dataKey="submitted" name="提交" stroke="#2b5cff" strokeWidth={2} dot={false} />
                  <Line type="monotone" dataKey="approved" name="通过" stroke="#389e0d" strokeWidth={2} dot={false} />
                  <Line type="monotone" dataKey="rejected" name="打回" stroke="#cf1322" strokeWidth={2} dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </Card>
        </Col>
        <Col span={9}>
          <Card size="small" title="驳回原因 TOP" style={{ height: "100%" }}>
            {rejections.data?.reasons?.length ? (
              rejections.data.reasons.map((r) => (
                <div key={r.tag} style={{ display: "flex", justifyContent: "space-between", padding: "5px 0" }}>
                  <Tag color="red">{r.tag}</Tag>
                  <b>{r.count}</b>
                </div>
              ))
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无驳回" />
            )}
          </Card>
        </Col>
      </Row>

      <Card size="small" title="标注员情况">
        <Table
          rowKey="id"
          size="small"
          loading={annotators.isLoading}
          dataSource={annotators.data?.items ?? []}
          pagination={false}
          columns={[
            { title: "姓名", dataIndex: "name" },
            { title: "领取", dataIndex: "granted", sorter: (a: AnnotatorRow, b: AnnotatorRow) => a.granted - b.granted },
            { title: "已提交", dataIndex: "submitted", sorter: (a: AnnotatorRow, b: AnnotatorRow) => a.submitted - b.submitted },
            { title: "已通过", dataIndex: "approved" },
            { title: "已打回", dataIndex: "rejected" },
            { title: "待审核", dataIndex: "pending" },
            { title: "驳回率", dataIndex: "reject_rate", render: (v: number) => `${Number(v).toFixed(1)}%` },
            { title: "一次通过率", dataIndex: "first_pass_rate", render: (v: number) => `${Number(v).toFixed(1)}%` },
            { title: "平均分", dataIndex: "avg_total", render: (v: number) => Number(v).toFixed(1) },
            { title: "平均耗时", dataIndex: "avg_ms", render: (v: number) => (v > 0 ? `${Math.round(v / 1000)}s` : "-") },
            { title: "异常上报", dataIndex: "exceptions" },
          ]}
        />
      </Card>

      <Card size="small" title="文件夹进度">
        <Table
          rowKey="id"
          size="small"
          dataSource={folders.data?.items ?? []}
          pagination={false}
          columns={[
            { title: "文件夹", dataIndex: "name" },
            { title: "总数", dataIndex: "total", width: 80 },
            {
              title: "进度",
              render: (_: unknown, f: FolderDTO) => (
                <Progress percent={Math.round(((f.total - f.unassigned) / Math.max(1, f.total)) * 100)} size="small" />
              ),
            },
            { title: "待领取", dataIndex: "unassigned", width: 90 },
            { title: "待审核", dataIndex: "submitted", width: 90 },
            { title: "已通过", dataIndex: "approved", width: 90 },
            { title: "已打回", dataIndex: "rejected", width: 90 },
            {
              title: "通过率",
              width: 100,
              render: (_: unknown, f: FolderDTO) => {
                const done = f.approved + f.rejected + f.excluded;
                return done === 0 ? "-" : `${((f.approved / done) * 100).toFixed(0)}%`;
              },
            },
          ]}
        />
      </Card>

      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        口径：驳回率 = 打回次数 / 已审核次数；一次通过率 = 首次提交即通过的图片 / 已审核图片；平均耗时取前端从打开到提交的计时。数字均由服务端 SQL 聚合。
      </Typography.Text>
    </Space>
  );
}
