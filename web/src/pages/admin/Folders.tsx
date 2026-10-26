import { useState } from "react";
import { Button, Card, Form, Input, Modal, Popconfirm, Progress, Space, Table, Tag, Tooltip, message } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import type { FolderDTO } from "../../api/types";

export default function FoldersPage() {
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<FolderDTO | null>(null);
  const [form] = Form.useForm();
  const [renameForm] = Form.useForm();

  const folders = useQuery({
    queryKey: ["folders", "all"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders?include_archived=1"),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["folders"] });
    qc.invalidateQueries({ queryKey: ["claim-folders"] });
  };

  const create = useMutation({
    mutationFn: (body: { name: string; note: string }) => api.post("/folders", body),
    onSuccess: () => {
      message.success("已创建");
      setCreateOpen(false);
      form.resetFields();
      refresh();
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "创建失败"),
  });

  const update = useMutation({
    mutationFn: ({ id, body }: { id: number; body: unknown }) => api.patch(`/folders/${id}`, body),
    onSuccess: () => {
      message.success("已保存");
      setRenameTarget(null);
      refresh();
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "保存失败"),
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.del(`/folders/${id}`),
    onSuccess: () => {
      message.success("已删除");
      refresh();
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "删除失败"),
  });

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Card
        size="small"
        title={`文件夹（${folders.data?.items?.length ?? 0}）`}
        extra={
          <Button type="primary" onClick={() => setCreateOpen(true)}>
            新建文件夹
          </Button>
        }
      >
        <Table
          rowKey="id"
          size="small"
          loading={folders.isLoading}
          dataSource={folders.data?.items ?? []}
          pagination={false}
          columns={[
            {
              title: "名称",
              dataIndex: "name",
              render: (v: string, r: FolderDTO) => (
                <Space>
                  <b>{v}</b>
                  {r.archived && <Tag>已归档</Tag>}
                </Space>
              ),
            },
            { title: "备注", dataIndex: "note" },
            { title: "总张数", dataIndex: "total", width: 90 },
            {
              title: "进度",
              width: 180,
              render: (_: unknown, r: FolderDTO) => (
                <Progress
                  percent={Math.round(((r.approved + r.excluded) / Math.max(1, r.total)) * 100)}
                  size="small"
                  format={() => `${r.approved + r.excluded}/${r.total}`}
                />
              ),
            },
            { title: "待领取", dataIndex: "unassigned", width: 80 },
            { title: "待审核", dataIndex: "submitted", width: 80 },
            { title: "已通过", dataIndex: "approved", width: 80 },
            { title: "已打回", dataIndex: "rejected", width: 80 },
            { title: "创建人", dataIndex: "creator", width: 110 },
            {
              title: "操作",
              width: 200,
              render: (_: unknown, r: FolderDTO) => (
                <Space>
                  <a
                    onClick={() => {
                      setRenameTarget(r);
                      renameForm.setFieldsValue({ name: r.name, note: r.note });
                    }}
                  >
                    编辑
                  </a>
                  <Tooltip title={!r.archived && r.approved + r.excluded < r.total ? "文件夹内还有未处理完成的图片，全部审核通过或确认无效后才可归档" : undefined}>
                    <Button type="link" size="small"
                      disabled={update.isPending || (!r.archived && r.approved + r.excluded < r.total)}
                      onClick={() => update.mutate({ id: r.id, body: { archived: !r.archived } })}>
                      {r.archived ? "取消归档" : "归档"}
                    </Button>
                  </Tooltip>
                  <Popconfirm
                    title="仅空文件夹可删除，确定？"
                    onConfirm={() => remove.mutate(r.id)}
                  >
                    <a style={{ color: "#cf1322" }}>删除</a>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Modal
        open={createOpen}
        title="新建文件夹"
        okText="创建"
        confirmLoading={create.isPending}
        onCancel={() => setCreateOpen(false)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" onFinish={(v) => create.mutate(v)}>
          <Form.Item name="name" label="名称" rules={[{ required: true, message: "请输入名称" }]}>
            <Input placeholder="例如 20261022 或 三年级二班" maxLength={64} />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!renameTarget}
        title="编辑文件夹"
        onCancel={() => setRenameTarget(null)}
        onOk={() => renameForm.submit()}
        confirmLoading={update.isPending}
        destroyOnHidden
      >
        <Form
          form={renameForm}
          layout="vertical"
          onFinish={(v) => renameTarget && update.mutate({ id: renameTarget.id, body: v })}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  );
}
