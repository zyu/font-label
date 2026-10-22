import { useState } from "react";
import { Button, Card, Form, Input, Modal, Select, Space, Table, Tag, Typography, message } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import type { User } from "../../api/types";

const ROLE_LABEL: Record<string, string> = { admin: "管理员", reviewer: "审核员", annotator: "标注员" };

export default function UsersPage() {
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [newPassword, setNewPassword] = useState<{ username: string; password: string } | null>(null);
  const [form] = Form.useForm();

  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => api.get<{ items: User[]; total: number }>("/users?page_size=100"),
  });

  const create = useMutation({
    mutationFn: (body: unknown) => api.post("/users", body),
    onSuccess: () => {
      message.success("已创建，初始密码需在首次登录时修改");
      setCreateOpen(false);
      form.resetFields();
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "创建失败"),
  });

  const patch = useMutation({
    mutationFn: ({ id, body }: { id: number; body: unknown }) => api.patch(`/users/${id}`, body),
    onSuccess: () => {
      message.success("已更新");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "更新失败"),
  });

  const reset = useMutation({
    mutationFn: (row: User) => api.post<{ new_password: string }>(`/users/${row.id}/reset-password`),
    onSuccess: (res, row) => {
      setNewPassword({ username: row.username, password: res.new_password });
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "重置失败"),
  });

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Card
        size="small"
        title={`用户（${users.data?.total ?? 0}）`}
        extra={
          <Button type="primary" onClick={() => setCreateOpen(true)}>
            新建账号
          </Button>
        }
      >
        <Table
          rowKey="id"
          size="small"
          loading={users.isLoading}
          dataSource={users.data?.items ?? []}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          columns={[
            { title: "账号", dataIndex: "username" },
            { title: "姓名", dataIndex: "display_name" },
            { title: "角色", dataIndex: "role", render: (v: string) => <Tag color={v === "admin" ? "purple" : v === "reviewer" ? "blue" : "green"}>{ROLE_LABEL[v]}</Tag> },
            { title: "状态", dataIndex: "status", render: (v: string) => (v === "active" ? <Tag color="green">启用</Tag> : <Tag color="red">停用</Tag>) },
            {
              title: "最近登录",
              dataIndex: "last_login_at",
              render: (v: string) => (v ? new Date(v).toLocaleString("zh-CN") : "从未登录"),
            },
            {
              title: "操作",
              width: 260,
              render: (_: unknown, row: User) => (
                <Space wrap>
                  <Select
                    size="small"
                    value={row.role}
                    style={{ width: 96 }}
                    onChange={(v) => patch.mutate({ id: row.id, body: { role: v } })}
                    options={[
                      { label: "标注员", value: "annotator" },
                      { label: "审核员", value: "reviewer" },
                      { label: "管理员", value: "admin" },
                    ]}
                  />
                  <Button size="small" onClick={() => patch.mutate({ id: row.id, body: { status: row.status === "active" ? "disabled" : "active" } })}>
                    {row.status === "active" ? "停用" : "启用"}
                  </Button>
                  <Button size="small" danger onClick={() => reset.mutate(row)}>
                    重置密码
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Modal
        open={createOpen}
        title="新建账号"
        okText="创建"
        confirmLoading={create.isPending}
        onCancel={() => setCreateOpen(false)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" initialValues={{ role: "annotator" }} onFinish={(v) => create.mutate(v)}>
          <Form.Item name="username" label="账号" rules={[{ required: true, min: 3, message: "至少 3 个字符" }]}>
            <Input placeholder="登录账号，例如 wang001" />
          </Form.Item>
          <Form.Item name="display_name" label="姓名" rules={[{ required: true }]}>
            <Input placeholder="显示名，例如 王老师" />
          </Form.Item>
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select
              options={[
                { label: "标注员（领取标注）", value: "annotator" },
                { label: "审核员（审核打回）", value: "reviewer" },
                { label: "管理员（全部权限）", value: "admin" },
              ]}
            />
          </Form.Item>
          <Form.Item name="password" label="初始密码" rules={[{ required: true, min: 8, message: "至少 8 位" }]}>
            <Input.Password placeholder="至少 8 位，首次登录会要求修改" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal open={!!newPassword} title="密码已重置" footer={null} onCancel={() => setNewPassword(null)}>
        <Typography.Paragraph>
          账号 <b>{newPassword?.username}</b> 的新密码：
        </Typography.Paragraph>
        <Typography.Paragraph copyable code style={{ fontSize: 16 }}>
          {newPassword?.password}
        </Typography.Paragraph>
        <Typography.Text type="secondary">请转告本人，登录后会强制修改密码。</Typography.Text>
      </Modal>
    </Space>
  );
}
