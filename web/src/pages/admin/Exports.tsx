import { useState } from "react";
import {
  Button,
  Card,
  DatePicker,
  Form,
  Segmented,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { DownloadOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { api, downloadBlob, downloadExportByID } from "../../api/client";
import type { FolderDTO, User } from "../../api/types";

interface Job {
  id: number;
  format: string;
  status: string;
  row_count: number;
  error: string;
  created_at: string;
  expires_at: string | null;
}

export default function ExportsPage() {
  const [format, setFormat] = useState("xlsx");
  const [status, setStatus] = useState("approved");
  const [folders, setFolders] = useState<number[]>([]);
  const [annotators, setAnnotators] = useState<number[]>([]);
  const [range, setRange] = useState<[dayjs.Dayjs, dayjs.Dayjs] | null>(null);
  const [dateField, setDateField] = useState("created");
  const [busy, setBusy] = useState(false);

  const folderList = useQuery({
    queryKey: ["folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders"),
  });
  const userList = useQuery({
    queryKey: ["users"],
    queryFn: () => api.get<{ items: User[] }>("/users?page_size=100"),
  });
  const jobs = useQuery({
    queryKey: ["export-jobs"],
    queryFn: () => api.get<{ items: Job[] }>("/exports"),
  });

  async function doExport() {
    setBusy(true);
    try {
      const res = await api.exportFile({
        format,
        filters: {
          folders,
          annotators,
          status,
          date_field: dateField,
          from: range?.[0] ? range[0].format("YYYY-MM-DD") : "",
          to: range?.[1] ? range[1].format("YYYY-MM-DD") : "",
        },
      });
      if (res.blob) {
        downloadBlob(res.blob, `font-label_${dayjs().format("YYYYMMDD_HHmm")}.${format}`);
        message.success("已开始下载");
      } else if (res.jobId) {
        message.info(`数据量较大，已创建导出任务 #${res.jobId}，完成后在下方下载`);
        jobs.refetch();
      }
    } catch (e) {
      message.error(e instanceof Error ? e.message : "导出失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Card size="small" title="导出标注结果">
        <Form layout="vertical" style={{ maxWidth: 900 }}>
          <Form.Item label="导出范围" style={{ marginBottom: 12 }}>
            <Space wrap size={12}>
              <Select
                mode="multiple"
                allowClear
                placeholder="全部文件夹"
                style={{ minWidth: 260 }}
                value={folders}
                onChange={setFolders}
                options={(folderList.data?.items ?? []).map((f) => ({ label: f.name, value: f.id }))}
              />
              <Select
                mode="multiple"
                allowClear
                placeholder="全部标注员"
                style={{ minWidth: 220 }}
                value={annotators}
                onChange={setAnnotators}
                options={(userList.data?.items ?? []).map((u) => ({ label: u.display_name, value: u.id }))}
              />
              <DatePicker.RangePicker value={range} onChange={(v) => setRange(v as never)} />
              <Segmented
                value={dateField}
                onChange={(v) => setDateField(String(v))}
                options={[
                  { label: "按上传日期", value: "created" },
                  { label: "按提交日期", value: "submitted" },
                ]}
              />
            </Space>
          </Form.Item>
          <Form.Item label="状态与格式" style={{ marginBottom: 12 }}>
            <Space wrap size={12}>
              <Segmented
                value={status}
                onChange={(v) => setStatus(String(v))}
                options={[
                  { label: "仅已通过", value: "approved" },
                  { label: "已审核（含打回）", value: "reviewed" },
                  { label: "全部", value: "all" },
                ]}
              />
              <Segmented
                value={format}
                onChange={(v) => setFormat(String(v))}
                options={[
                  { label: "Excel (.xlsx)", value: "xlsx" },
                  { label: "CSV", value: "csv" },
                  { label: "JSON", value: "json" },
                  { label: "训练集 JSONL", value: "jsonl" },
                ]}
              />
              <Button type="primary" icon={<DownloadOutlined />} loading={busy} onClick={doExport}>
                导出
              </Button>
            </Space>
          </Form.Item>
        </Form>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Excel 列：文件夹、服务器文件名、原始文件名、序号、标注员、提交时间、五个维度分数（白平衡/黑平衡/参差/结构/控笔）、总分、等级、点评、状态、审核员、审核时间、驳回次数、异常原因、图片相对路径。
          <br />
          训练集 JSONL：每行 <code>{`{"messages":[{user:"<image>请评价…"},{assistant:"白平衡（匀）：20/20 …总分：90/100 档位：优秀 总评：…"}],"images":["images/<文件夹ID>/<服务器文件名>"]}`}</code>；
          只输出<b>审核通过</b>与<b>无效图</b>两类样本（未审核的不进训练集），无效图为「无效：原因，建议重拍」。
          超过 5000 行会自动转异步任务。
        </Typography.Text>
      </Card>

      <Card size="small" title="导出记录（异步任务）">
        <Table
          rowKey="id"
          size="small"
          dataSource={jobs.data?.items ?? []}
          pagination={false}
          columns={[
            { title: "#", dataIndex: "id", width: 70 },
            { title: "格式", dataIndex: "format", width: 90 },
            {
              title: "状态",
              dataIndex: "status",
              width: 110,
              render: (v: string) =>
                v === "done" ? <Tag color="green">已完成</Tag> : v === "failed" ? <Tag color="red">失败</Tag> : <Tag color="processing">{v}</Tag>,
            },
            { title: "行数", dataIndex: "row_count", width: 90 },
            { title: "创建时间", dataIndex: "created_at", render: (v: string) => new Date(v).toLocaleString("zh-CN") },
            { title: "过期时间", dataIndex: "expires_at", render: (v: string | null) => (v ? new Date(v).toLocaleString("zh-CN") : "-") },
            { title: "错误", dataIndex: "error" },
            {
              title: "操作",
              width: 120,
              render: (_: unknown, row: Job) =>
                row.status === "done" ? (
                  <a onClick={() => downloadExportByID(row.id, `font-label_${row.id}.${row.format}`)}>下载</a>
                ) : (
                  "-"
                ),
            },
          ]}
        />
      </Card>
    </Space>
  );
}
