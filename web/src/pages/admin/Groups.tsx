import { useState } from "react";
import { Alert, Button, Card, Form, Input, Modal, Segmented, Select, Space, Switch, Table, Tag, Typography, message } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { FolderDTO, ImageDTO, Page, User } from "../../api/types";

interface Group {
  id: number;
  name: string;
  archived: boolean;
  member_ids: number[];
}

type GroupInput = Omit<Group, "id">;

async function getGroupUsers() {
  const users: User[] = [];
  for (let page = 1; ; page++) {
    const result = await api.get<Page<User>>(`/users?page=${page}&page_size=100`);
    users.push(...result.items);
    if (users.length >= result.total || result.items.length === 0) {
      return users.filter((user) => user.role === "annotator" || user.role === "reviewer");
    }
  }
}

export default function Groups() {
  const cache = useQueryClient();
  const [form] = Form.useForm<GroupInput>();
  const [editing, setEditing] = useState<number | null>(null);
  const [mode, setMode] = useState("folder");
  const [folder, setFolder] = useState<number>();
  const [groups, setGroups] = useState<number[]>([]);
  const [selected, setSelected] = useState<number[]>([]);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);

  const groupList = useQuery({ queryKey: ["groups"], queryFn: () => api.get<{ items: Group[] }>("/groups") });
  const users = useQuery({ queryKey: ["group-users"], queryFn: getGroupUsers });
  const folders = useQuery({
    queryKey: ["dispatch-folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/folders"),
  });
  const sources = useQuery({
    queryKey: ["dispatch-sources", folder, search, page],
    enabled: mode === "images" && !!folder,
    queryFn: () => api.get<Page<ImageDTO>>(`/images?sources=1&folder_id=${folder}&q=${encodeURIComponent(search)}&page=${page}&page_size=20`),
  });
  const save = useMutation({
    mutationFn: (body: GroupInput) => editing ? api.put(`/groups/${editing}`, body) : api.post("/groups", body),
    onSuccess: () => {
      message.success("分组已保存");
      setEditing(null);
      cache.invalidateQueries({ queryKey: ["groups"] });
      cache.invalidateQueries({ queryKey: ["work-mode"] });
    },
    onError: (e) => message.error(e.message),
  });
  const dispatch = useMutation({
    mutationFn: () => api.post<{ created: number }>("/groups/dispatch", {
      group_ids: groups,
      ...(mode === "images" ? { image_ids: selected } : { folder_id: folder }),
    }),
    onSuccess: ({ created }) => {
      message.success(created ? `已下发 ${created} 条标注任务` : "所选图片已下发给这些分组");
      setSelected([]);
      for (const queryKey of ["dispatch-folders", "my-tasks", "my-counts"]) {
        cache.invalidateQueries({ queryKey: [queryKey] });
      }
    },
    onError: (e) => message.error(e.message),
  });

  const userById = new Map(users.data?.map((user) => [user.id, user]));
  const memberOptions = users.data?.map((user) => ({
    value: user.id,
    label: `${user.display_name} · ${user.role === "annotator" ? "标注员" : "审核员"}${user.status === "disabled" ? "（已停用）" : ""}`,
  }));
  const edit = (group?: Group) => {
    form.setFieldsValue(group ?? { name: "", member_ids: [], archived: false });
    setEditing(group?.id ?? 0);
  };

  return (
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <Card title="人员分组" extra={<Button type="primary" onClick={() => edit()}>新建分组</Button>}>
        {(groupList.error || users.error) && <Alert type="error" message={(groupList.error ?? users.error)?.message} />}
        <Table<Group>
          rowKey="id"
          size="small"
          loading={groupList.isLoading || users.isLoading}
          dataSource={groupList.data?.items ?? []}
          columns={[
            { title: "组名", dataIndex: "name" },
            ...(["annotator", "reviewer"] as const).map((role) => ({
              title: role === "annotator" ? "标注员" : "审核员",
              render: (_: unknown, group: Group) => group.member_ids.map((id) => userById.get(id))
                .filter((user) => user?.role === role)
                .map((user) => <Tag key={user!.id}>{user!.display_name}{user!.status === "disabled" ? "（已停用）" : ""}</Tag>),
            })),
            { title: "状态", render: (_, group) => <Tag color={group.archived ? "default" : "green"}>{group.archived ? "已归档" : "启用"}</Tag> },
            { title: "操作", render: (_, group) => <Button size="small" onClick={() => edit(group)}>编辑</Button> },
          ]}
        />
      </Card>

      <Card title="下发标注任务">
        {(folders.error || sources.error) && <Alert type="error" message={(folders.error ?? sources.error)?.message} />}
        <Space wrap>
          <Segmented value={mode} onChange={setMode} options={[
            { label: "整个文件夹", value: "folder" }, { label: "选择图片", value: "images" },
          ]} />
          <Select showSearch optionFilterProp="label" placeholder="选择文件夹" style={{ width: 200 }}
            value={folder} onChange={(value) => { setFolder(value); setPage(1); setSelected([]); }}
            options={folders.data?.items.map((item) => ({ label: item.name, value: item.id }))} />
          <Select mode="multiple" optionFilterProp="label" placeholder="选择目标分组" style={{ minWidth: 220 }}
            value={groups} onChange={setGroups}
            options={groupList.data?.items.filter((item) => !item.archived).map((item) => ({ label: item.name, value: item.id }))} />
          <Button type="primary" loading={dispatch.isPending} disabled={!folder || !groups.length || (mode === "images" && !selected.length)}
            onClick={() => Modal.confirm({
              title: "下发标注任务",
              content: `将${mode === "folder" ? "文件夹内的全部原图" : `选中的 ${selected.length} 张图片`}下发给 ${groups.length} 个组？`,
              onOk: () => dispatch.mutateAsync().then(() => undefined),
            })}>下发任务</Button>
        </Space>
        {mode === "images" && folder && (
          <>
            <Space style={{ margin: "16px 0" }}>
              <Input.Search allowClear placeholder="按文件名或样本编号搜索" onSearch={(value) => { setSearch(value); setPage(1); }} />
              <Typography.Text type="secondary">已选 {selected.length} 张</Typography.Text>
            </Space>
            <Table<ImageDTO> size="small" rowKey="id" loading={sources.isFetching} dataSource={sources.data?.items ?? []}
              rowSelection={{ selectedRowKeys: selected, preserveSelectedRowKeys: true, onChange: (keys) => setSelected(keys.map(Number)) }}
              pagination={{ current: page, pageSize: 20, total: sources.data?.total, onChange: setPage, showSizeChanger: false }}
              columns={[
                { title: "图片", width: 85, render: (_, row) => <img className="thumb" src={row.thumb_url} alt={row.orig_name} /> },
                { title: "样本编号", dataIndex: "sample_id" },
                { title: "文件名", dataIndex: "orig_name" },
              ]}
            />
          </>
        )}
      </Card>

      <Modal title={editing ? "编辑分组" : "新建分组"} open={editing !== null} onCancel={() => setEditing(null)}
        onOk={() => form.submit()} confirmLoading={save.isPending}>
        <Form form={form} layout="vertical" onFinish={(values) => save.mutate(values)}>
          <Form.Item name="name" label="组名" rules={[{ required: true, whitespace: true }, { max: 64 }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item name="member_ids" label="成员">
            <Select mode="multiple" optionFilterProp="label" options={memberOptions} loading={users.isLoading} />
          </Form.Item>
          {editing !== 0 && <Form.Item name="archived" label="归档" valuePropName="checked"><Switch /></Form.Item>}
        </Form>
      </Modal>
    </Space>
  );
}
