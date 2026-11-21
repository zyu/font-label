import ListTable from "../../components/ListTable";
import { useState } from "react";
import {
  Button,
  Card,
  DatePicker,
  Form,
  Segmented,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from "antd";
import { CloudUploadOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import { api } from "../../api/client";
import type { FolderDTO, Page, User } from "../../api/types";

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

export default function ExportsPage() {
  const [page, setPage] = useState(1);
  const [folders, setFolders] = useState<number[]>([]);
  const [annotators, setAnnotators] = useState<number[]>([]);
  const [range, setRange] = useState<[dayjs.Dayjs, dayjs.Dayjs] | null>(null);
  const [dateField, setDateField] = useState("created");
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
    queryKey: ["export-jobs", page],
    queryFn: () => api.get<Page<Job> & { oss_enabled: boolean }>(`/exports?page=${page}&page_size=20`),
    refetchInterval: (query) => query.state.data?.items.some((job) => job.status === "pending" || job.status === "running") ? 2000 : false,
  });

  const filters = {
    folders,
    annotators,
    status: "approved",
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
        </Form>
        <Space wrap size={10} style={{ marginBottom: 12 }}>
          <Button icon={<CloudUploadOutlined />} loading={uploading}
            disabled={!jobs.data?.oss_enabled} onClick={uploadToOSS}>
            上传结果和图片到 OSS
          </Button>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {jobs.data?.oss_enabled
              ? "按当前筛选打包训练集和原图，后台上传 ZIP。"
              : jobs.data ? "OSS 上传尚未启用，请检查服务端配置。" : "正在检查 OSS 配置"}
          </Typography.Text>
        </Space>
        <br />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          ZIP 仅包含 data.jsonl 和原图，只收录已走完流程且五维等级、评语齐全的正常标注。
        </Typography.Text>
      </Card>

      <Card size="small" title="导出记录">
        <ListTable
          rowKey="id"
          size="small"
          dataSource={jobs.data?.items ?? []}
          pagination={{ current: page, pageSize: 20, total: jobs.data?.total, onChange: setPage }}
          columns={[
            { title: "#", dataIndex: "id", width: 70 },
            {
              title: "格式", dataIndex: "format", width: 100,
              render: () => "OSS ZIP",
            },
            {
              title: "状态",
              dataIndex: "status",
              width: 110,
              render: (value: string) => {
                if (value === "done") return <Tag color="green">已上传</Tag>;
                if (value === "failed") return <Tag color="red">失败</Tag>;
                return <Tag color="processing">{value === "pending" ? "等待中" : "打包或上传中"}</Tag>;
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
                return (
                  <Space wrap>
                    {row.object_url && <a href={row.object_url} target="_blank" rel="noopener noreferrer">对象地址</a>}
                    {row.object_key && <Typography.Text copyable={{ text: row.object_key, tooltips: ["复制对象键", "已复制"] }} />}
                  </Space>
                );

              },
            },
          ]}
        />
      </Card>
    </Space>
  );
}
