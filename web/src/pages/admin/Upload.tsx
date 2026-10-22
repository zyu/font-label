import { useState } from "react";
import { Alert, Button, Card, Input, Select, Space, Table, Tag, Typography, Upload, message } from "antd";
import { InboxOutlined } from "@ant-design/icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import type { FolderDTO } from "../../api/types";

interface UploadItem {
  file: string;
  ok: boolean;
  image_id?: number;
  stored_name?: string;
  width?: number;
  height?: number;
  reason?: string;
  message?: string;
  duplicate_of?: number;
}

export default function UploadPage() {
  const qc = useQueryClient();
  const [files, setFiles] = useState<File[]>([]);
  const [folderId, setFolderId] = useState<number | undefined>();
  const [folderName, setFolderName] = useState("");
  const [busy, setBusy] = useState(false);
  const [items, setItems] = useState<UploadItem[]>([]);
  const [summary, setSummary] = useState<{ ok: number; duplicate: number; failed: number } | null>(null);

  const folders = useQuery({
    queryKey: ["folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders"),
  });

  async function doUpload() {
    if (files.length === 0) {
      message.warning("请先选择图片");
      return;
    }
    const form = new FormData();
    files.forEach((f) => form.append("files", f, f.name));
    if (folderId) form.append("folder_id", String(folderId));
    else if (folderName.trim()) form.append("folder_name", folderName.trim());
    setBusy(true);
    try {
      const res = await api.upload(form);
      setItems(res.items as UploadItem[]);
      setSummary(res.summary);
      message.success(`成功 ${res.summary.ok} 张，重复 ${res.summary.duplicate} 张，失败 ${res.summary.failed} 张`);
      setFiles([]);
      qc.invalidateQueries({ queryKey: ["folders"] });
      qc.invalidateQueries({ queryKey: ["claim-folders"] });
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : "上传失败");
    } finally {
      setBusy(false);
    }
  }

  const failedOnly = () => {
    message.info("请重新选择失败的文件上传（重复与格式错误已在下表标出原因）");
  };

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        title="命名规则：服务器按「当天日期_三位序号」重命名保存"
        description="例如 20261022_001.png、20261022_002.jpg，序号按天全局自增；同名图片（sha256 相同）会被跳过。照片会按 EXIF 自动转正并剥离元数据。"
      />

      <Card size="small" title="上传目标">
        <Space wrap size={16}>
          <Space>
            <Typography.Text type="secondary">选择已有文件夹</Typography.Text>
            <Select
              allowClear
              placeholder="不选则用下面的名称"
              style={{ minWidth: 240 }}
              value={folderId}
              onChange={setFolderId}
              options={(folders.data?.items ?? []).map((f) => ({ label: f.name, value: f.id }))}
            />
          </Space>
          <Space>
            <Typography.Text type="secondary">或新建文件夹名</Typography.Text>
            <Input
              placeholder="留空 = 当天日期，例如 20261022"
              style={{ width: 260 }}
              value={folderName}
              onChange={(e) => setFolderName(e.target.value)}
              disabled={!!folderId}
            />
          </Space>
        </Space>
      </Card>

      <Card size="small" title="选择图片（可多选、可拖入整个文件夹）">
        <Upload.Dragger
          multiple
          directory
          accept=".jpg,.jpeg,.png,.webp,.gif"
          beforeUpload={() => false}
          fileList={files.map((f, i) => ({ uid: String(i), name: f.name, status: "done" as const }))}
          onRemove={(file) => setFiles((prev) => prev.filter((f) => f.name !== file.name))}
          onChange={(info) => {
            const next = info.fileList.map((f) => f.originFileObj).filter(Boolean) as File[];
            setFiles(next);
          }}
          onDrop={(e) => {
            const dropped = Array.from(e.dataTransfer.files || []);
            if (dropped.length) setFiles(dropped);
          }}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined />
          </p>
          <p className="ant-upload-text">点击或拖拽图片 / 整个文件夹到这里</p>
          <p className="ant-upload-hint">支持 jpg / png / webp / gif，单张 ≤ 20MB</p>
        </Upload.Dragger>

        <Space style={{ marginTop: 14 }}>
          <Button type="primary" loading={busy} onClick={doUpload} disabled={files.length === 0} data-test="do-upload">
            开始上传（{files.length} 张）
          </Button>
          <Button onClick={() => setFiles([])} disabled={files.length === 0}>
            清空
          </Button>
          {summary && (
            <Space size={6}>
              <Tag color="green">成功 {summary.ok}</Tag>
              <Tag color="orange">重复 {summary.duplicate}</Tag>
              <Tag color="red">失败 {summary.failed}</Tag>
              {summary.failed > 0 && <a onClick={failedOnly}>怎么处理？</a>}
            </Space>
          )}
        </Space>
      </Card>

      {items.length > 0 && (
        <Card size="small" title="本次上传结果">
          <Table
            rowKey={(r) => r.file + (r.image_id ?? 0)}
            size="small"
            dataSource={items}
            pagination={false}
            columns={[
              { title: "原文件名", dataIndex: "file" },
              {
                title: "结果",
                width: 100,
                render: (_: unknown, r: UploadItem) =>
                  r.ok ? <Tag color="green">成功</Tag> : <Tag color="red">{r.reason}</Tag>,
              },
              { title: "服务器文件名", dataIndex: "stored_name" },
              {
                title: "尺寸",
                width: 120,
                render: (_: unknown, r: UploadItem) => (r.width ? `${r.width}×${r.height}` : "-"),
              },
              { title: "说明", dataIndex: "message" },
            ]}
          />
        </Card>
      )}
    </Space>
  );
}
