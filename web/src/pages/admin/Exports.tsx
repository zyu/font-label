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
import { CloudUploadOutlined, DownloadOutlined } from "@ant-design/icons";
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
  destination?: "download" | "oss";
  object_url?: string;
  object_key?: string;
}

const fileExtension = (format: string) => format === "jsonl" ? "zip" : format;

export default function ExportsPage() {
  const [format, setFormat] = useState("xlsx");
  const [status, setStatus] = useState("approved");
  const [folders, setFolders] = useState<number[]>([]);
  const [annotators, setAnnotators] = useState<number[]>([]);
  const [range, setRange] = useState<[dayjs.Dayjs, dayjs.Dayjs] | null>(null);
  const [dateField, setDateField] = useState("created");
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(false);

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
    queryFn: () => api.get<{ items: Job[]; oss_enabled: boolean }>("/exports"),
    refetchInterval: (query) => query.state.data?.items.some((job) => job.status === "pending" || job.status === "running") ? 2000 : false,
  });

  const filters = {
    folders,
    annotators,
    status,
    date_field: dateField,
    from: range?.[0]?.format("YYYY-MM-DD") ?? "",
    to: range?.[1]?.format("YYYY-MM-DD") ?? "",
  };

  async function uploadToOSS() {
    setUploading(true);
    try {
      const { job_id } = await api.post<{ job_id: number }>("/exports/oss", { filters });
      message.info(`已创建 OSS 上传任务 #${job_id}，可在下方查看状态`);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "创建 OSS 上传任务失败");
    } finally {
      setUploading(false);
      await jobs.refetch();
    }
  }

  async function doExport() {
    setBusy(true);
    try {
      const res = await api.exportFile({ format, filters });
      if (res.blob) {
        downloadBlob(res.blob, `font-label_${dayjs().format("YYYYMMDD_HHmm")}.${fileExtension(format)}`);
        message.success("已开始下载");
      } else if (res.jobId) {
        message.info(`数据量较大，已创建导出任务 #${res.jobId}，完成后在下方下载`);
      }
    } catch (e) {
      message.error(e instanceof Error ? e.message : "导出失败");
    } finally {
      setBusy(false);
      await jobs.refetch();
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
                  { label: "训练集 ZIP", value: "jsonl" },
                ]}
              />
              <Button type="primary" icon={<DownloadOutlined />} loading={busy} onClick={doExport}>
                导出
              </Button>
            </Space>
          </Form.Item>
        </Form>
        <Space wrap size={10} style={{ marginBottom: 12 }}>
          <Button icon={<CloudUploadOutlined />} loading={uploading}
            disabled={!jobs.data?.oss_enabled} onClick={uploadToOSS}>
            上传结果和图片到 OSS
          </Button>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {jobs.data?.oss_enabled
              ? "按当前筛选打包标注 JSON 和原图，后台上传 ZIP。"
              : jobs.data ? "OSS 上传尚未启用，请检查服务端配置。" : "正在检查 OSS 配置"}
          </Typography.Text>
        </Space>
        <br />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          「仅已通过」只导出审核通过的评分。需要包含无效图时，请选择「已审核（含打回）」或「全部」。
          <br />
          Excel 列：文件夹、服务器文件名、原始文件名、序号、标注员、提交时间、五个维度分数（白平衡/黑平衡/参差/结构/控笔）、总分、等级、点评、状态、审核员、审核时间、驳回次数、异常原因、图片相对路径。
          <br />
          训练集 ZIP：一个压缩包内包含一个 <code>data.jsonl</code> 和 <code>images/</code> 目录。
          N 条样本对应 JSONL 的 N 行及 N 张图片，每行包含 <code>messages</code> 与 <code>images</code>，图片路径与压缩包内一致，文件名全局唯一。
          只输出<b>审核通过</b>与<b>无效图</b>两类样本（未审核的不进训练集），无效图为「无效：原因，建议重拍」。
          超过 5000 行会自动转异步任务。
        </Typography.Text>
      </Card>

      <Card size="small" title="导出记录">
        <Table
          rowKey="id"
          size="small"
          dataSource={jobs.data?.items ?? []}
          pagination={false}
          columns={[
            { title: "#", dataIndex: "id", width: 70 },
            {
              title: "格式", dataIndex: "format", width: 100,
              render: (value: string, row: Job) => row.destination === "oss" ? "OSS ZIP" :
                value === "jsonl" ? "训练集 ZIP" : value.toUpperCase(),
            },
            {
              title: "状态",
              dataIndex: "status",
              width: 110,
              render: (value: string, row: Job) => {
                if (value === "done") return <Tag color="green">{row.destination === "oss" ? "已上传" : "已完成"}</Tag>;
                if (value === "failed") return <Tag color="red">失败</Tag>;
                return <Tag color="processing">{value === "pending" ? "等待中" : row.destination === "oss" ? "打包或上传中" : "导出中"}</Tag>;
              },
            },
            { title: "行数", dataIndex: "row_count", width: 90 },
            { title: "创建时间", dataIndex: "created_at", render: (v: string) => new Date(v).toLocaleString("zh-CN") },
            { title: "过期时间", dataIndex: "expires_at", render: (v: string | null) => (v ? new Date(v).toLocaleString("zh-CN") : "-") },
            { title: "错误", dataIndex: "error" },
            {
              title: "操作",
              width: 120,
              render: (_: unknown, row: Job) => {
                if (row.status !== "done") return "—";
                if (row.destination === "oss") return (
                  <Space wrap>
                    {row.object_url && <a href={row.object_url} target="_blank" rel="noopener noreferrer">对象地址</a>}
                    {row.object_key && <Typography.Text copyable={{ text: row.object_key, tooltips: ["复制对象键", "已复制"] }} />}
                  </Space>
                );
                if (row.expires_at && dayjs(row.expires_at).isBefore(dayjs())) return "已过期";
                return <a onClick={() => downloadExportByID(row.id, `font-label_${row.id}.${fileExtension(row.format)}`)
                  .catch((error) => message.error(error instanceof Error ? error.message : "下载失败"))}>下载</a>;
              },
            },
          ]}
        />
      </Card>
    </Space>
  );
}
