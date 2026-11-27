import ListTable from "../components/ListTable";
import { useState } from "react";
import { Alert, Button, Card, Descriptions, Drawer, Form, Input, InputNumber, Segmented, Select, Space, Tabs, Tag, message } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { Page } from "../api/types";
import { useAuth } from "../auth";
import ImageViewer from "../components/ImageViewer";
import { exceptionReasonLabel } from "../lib/settings";

interface GradeDifference {
  dimension: string;
  label: string;
  own_grade: string;
  peer_grade: string;
  gap: number;
  own_comment: string;
  peer_comment: string;
  peer_group: string;
  peer_annotator: string;
}

interface QualityRecord {
  id: number;
  sample_id: string;
  image_id: number;
  image: string;
  group: string;
  original_url: string;
  review: "approve" | "reject";
  review_note: string;
  reviewer: string;
  annotator: string;
  version: number;
  annotation: {
    kind?: "score" | "exception";
    grades?: Record<string, string>;
    dim_comments?: Record<string, string>;
    comment?: string;
    exception_reasons?: string[];
    exception_note?: string;
  };
  grade_patch: Record<string, string> | null;
  grade_differences?: GradeDifference[];
  quality_result: "" | "pass" | "fail";
  quality_note: string;
  inspector: string;
  dispute_id: number;
  arbiter: string;
  ruling: "" | "return" | "approve";
  ruling_note: string;
  self_arbitrated?: boolean;
  awaiting_arbiter?: boolean;
  registered_at: string | null;
  resolved_at: string | null;
}

const qualityLabels = { pass: "合格", fail: "不合格" };
const rulingLabels = { approve: "通过", return: "退回" };
const timeLabel = (value: string | null) => value ? new Date(value).toLocaleString("zh-CN") : "—";

type SamplingPolicy = { sample_rate_bps: number; strata: string[] };

function QualityPolicy() {
  const policy = useQuery({
    queryKey: ["quality-policy"],
    queryFn: () => api.get<SamplingPolicy>("/quality-policy"),
  });
  const cache = useQueryClient();
  const save = useMutation({
    mutationFn: (values: SamplingPolicy) => api.put("/quality-policy", values),
    onSuccess: () => { message.success("抽检规则已保存"); cache.invalidateQueries({ queryKey: ["quality-policy"] }); },
    onError: (e) => message.error(e.message),
  });
  return (
    <Card title="抽检规则" size="small" loading={policy.isLoading}>
      {policy.error && <Alert type="error" message={policy.error.message} />}
      {policy.data && <Form<{ sample_rate: number; strata: string[] }> key={JSON.stringify(policy.data)} layout="inline"
        initialValues={{ sample_rate: policy.data.sample_rate_bps / 100, strata: policy.data.strata }}
        onFinish={(v) => save.mutate({ sample_rate_bps: Math.round(v.sample_rate * 100), strata: v.strata })}>
        <Form.Item name="sample_rate" label="抽检比例（%）" extra="默认 5%，每 20 张抽 1 张。设为 0 可暂停抽检，配置适用于后续审核。"
          rules={[{ required: true, type: "number", min: 0, max: 100 }]}>
          <InputNumber min={0} max={100} precision={2} />
        </Form.Item>
        <Form.Item name="strata" label="抽检分层" extra="留空时按固定样本序号抽检。选中时，各层按审核顺序独立计数。">
          <Select mode="multiple" allowClear placeholder="按固定样本序号" style={{ minWidth: 200 }} options={[
            { label: "分组", value: "group" }, { label: "文件夹", value: "folder" }, { label: "审核结论", value: "review" },
          ]} />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={save.isPending}>保存规则</Button>
      </Form>}
    </Card>
  );
}

export default function Quality() {
  const { user, me } = useAuth();
  const admin = user?.role === "admin";
  const [tab, setTab] = useState("samples");
  const [status, setStatus] = useState("pending");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<QualityRecord>();
  const [note, setNote] = useState("");
  const cache = useQueryClient();
  const disputes = tab === "disputes";
  const list = useQuery({
    queryKey: ["quality", user?.id, tab, status, page],
    queryFn: () => api.get<Page<QualityRecord>>(`/quality/${tab}?status=${status}&page=${page}&page_size=20`),
  });
  const decide = useMutation({
    mutationFn: ({ id, result }: { id: number; result: string }) => api.post<{ awaiting_arbiter?: boolean }>(`/quality/${tab}/${id}`, { result, note }),
    onSuccess: (outcome) => {
      message.success(outcome?.awaiting_arbiter ? "结论已保存，争议已挂起待分配独立仲裁员" : "结论已保存");
      setSelected(undefined);
      cache.invalidateQueries({ queryKey: ["quality"] });
    },
    onError: (e) => message.error(e.message),
  });
  const tasks = list.data?.items ?? [];
  const open = (record: QualityRecord) => { setSelected(record); setNote(""); };
  const completed = disputes ? selected?.ruling : selected?.quality_result;
  const canDecide = selected && !admin && !completed && !selected.awaiting_arbiter && !selected.self_arbitrated;

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      {admin && <QualityPolicy />}
      <Card>
        <Tabs activeKey={tab} onChange={(value) => { setTab(value); setPage(1); }}
          items={[{ key: "samples", label: "质检抽检" }, { key: "disputes", label: "争议仲裁登记" }]} />
        <Segmented value={status} onChange={(value) => { setStatus(value); setPage(1); }} options={[
          { label: disputes ? "待仲裁" : "待质检", value: "pending" },
          { label: disputes ? "已仲裁" : "已质检", value: "done" },
          { label: "全部", value: "all" },
        ]} style={{ marginBottom: 12 }} />
        {list.error && <Alert type="error" message={list.error.message} />}
        <ListTable<QualityRecord> rowKey="id" size="small" loading={list.isFetching} dataSource={tasks}
          pagination={{ current: page, pageSize: 20, total: list.data?.total, onChange: setPage, showSizeChanger: false }}
          columns={[
            ...(disputes ? [{ title: "登记编号", dataIndex: "dispute_id" }] : []),
            { title: "样本", render: (_, record) => <Button type="link" onClick={() => open(record)}>{record.sample_id} · {record.image}</Button> },
            { title: "组别", dataIndex: "group" },
            { title: "标注版本", dataIndex: "version" },
            { title: "审核结果", render: (_, row) => row.review === "approve" ? "通过" : "退回" },
            { title: "质检员", dataIndex: "inspector", render: (value: string, row) => row.grade_differences?.length ? "—" : value || "待分配" },
            { title: disputes ? "争议原因" : "质检结论", render: (_, row) => {
              if (row.grade_differences?.length) return "组间等级差异";
              if (disputes && row.quality_result === "fail") return "质检不合格";
              return row.quality_result ? qualityLabels[row.quality_result] : "待质检";
            } },
            ...(disputes ? [
              { title: "仲裁员", dataIndex: "arbiter", render: (value: string, row: QualityRecord) => <Space>{value || "待分配"}{row.self_arbitrated && <Tag color="red">同人质检与仲裁</Tag>}</Space> },
              { title: "仲裁状态", render: (_: unknown, row: QualityRecord) => <Tag color={row.ruling ? "green" : "orange"}>{row.ruling ? `已仲裁 · ${rulingLabels[row.ruling]}` : row.awaiting_arbiter ? "待分配独立仲裁员" : "待仲裁"}</Tag> },
              { title: "仲裁时间", render: (_: unknown, row: QualityRecord) => timeLabel(row.resolved_at) },
            ] : []),
          ]}
        />
      </Card>

      <Drawer width="min(980px, 100vw)" title={selected ? `${selected.sample_id} · ${selected.group}` : ""} open={!!selected}
        onClose={() => setSelected(undefined)}
        footer={canDecide && <Space wrap>
          {Object.entries(disputes ? rulingLabels : qualityLabels).map(([result, label]) => (
            <Button key={result} type="primary" danger={["fail", "return"].includes(result)} loading={decide.isPending}
              onClick={() => decide.mutate({ id: disputes ? selected.dispute_id : selected.id, result })}>{label}</Button>
          ))}
        </Space>}>
        {selected && <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <div className="quality-image-frame"><ImageViewer src={selected.original_url} alt={selected.image} resetKey={selected.image_id} /></div>
          <Descriptions bordered size="small" column={1}>
            <Descriptions.Item label="标注">{selected.annotator || "—"} · 第 {selected.version} 版</Descriptions.Item>
            {selected.annotation.kind === "exception" ? (
              <Descriptions.Item label="异常原因">
                {selected.annotation.exception_reasons?.map((key) => exceptionReasonLabel(key, me?.exception_reasons)).join("、")}
                {selected.annotation.exception_note || selected.annotation.comment}
              </Descriptions.Item>
            ) : me?.rubric.dims.map((dim) => (
              <Descriptions.Item key={dim.key} label={dim.label}>
                {selected.annotation.grades?.[dim.key] ?? "—"}
                {selected.grade_patch?.[dim.key] != null && ` · 审核修正为 ${selected.grade_patch[dim.key]}`}
                {selected.annotation.dim_comments?.[dim.key] && ` · ${selected.annotation.dim_comments[dim.key]}`}
              </Descriptions.Item>
            ))}
            <Descriptions.Item label="审核">{selected.reviewer} · {selected.review === "approve" ? "通过" : "退回"} {selected.review_note}</Descriptions.Item>
            {selected.quality_result && <Descriptions.Item label="质检">{selected.inspector} · {qualityLabels[selected.quality_result]} {selected.quality_note}</Descriptions.Item>}
            {!!selected.dispute_id && <Descriptions.Item label="争议登记">#{selected.dispute_id} · {timeLabel(selected.registered_at)}</Descriptions.Item>}
            {selected.ruling && <Descriptions.Item label="仲裁">{selected.arbiter} · {rulingLabels[selected.ruling]} · {timeLabel(selected.resolved_at)} {selected.ruling_note}</Descriptions.Item>}
          </Descriptions>
          {!!selected.grade_differences?.length && (
            <ListTable<GradeDifference>
              rowKey={(row) => `${row.peer_group}:${row.dimension}`}
              size="small"
              pagination={false}
              dataSource={selected.grade_differences}
              columns={[
                { title: "维度", dataIndex: "label" },
                { title: "本组原始标注", render: (_, row) => <><Tag>{row.own_grade}</Tag>{row.own_comment}</> },
                { title: "对组原始标注", render: (_, row) => (<>
                  <div>{row.peer_group} · {row.peer_annotator}</div>
                  <Tag>{row.peer_grade}</Tag>{row.peer_comment}
                </>) },
                { title: "档差", render: (_, row) => `相差 ${row.gap} 档` },
              ]}
            />
          )}
          {selected.awaiting_arbiter && <Alert type="warning" title="暂无独立仲裁员，已挂起待分配" />}
          {selected.self_arbitrated && <Alert type="warning" title="历史记录由同一人质检和仲裁" />}
          {canDecide && <Input.TextArea aria-label="处理备注" placeholder="处理备注" value={note}
            onChange={(event) => setNote(event.target.value)} maxLength={1000} />}
        </Space>}
      </Drawer>
    </Space>
  );
}
