import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  Empty,
  Input,
  Progress,
  Row,
  Segmented,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../api/client";
import { STATUS_COLOR, STATUS_LABEL, type ClaimResult, type FolderDTO, type ImageDTO, type Page } from "../api/types";
import { useAuth } from "../auth";

const TABS = [
  { label: "待完成", value: "active" },
  { label: "已驳回", value: "rejected" },
  { label: "待审核", value: "submitted" },
  { label: "已通过", value: "approved" },
];

export default function WorkbenchPage() {
  const { me } = useAuth();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [tab, setTab] = useState("active");
  const [count, setCount] = useState(me?.limits.claim_default_count ?? 10);

  const folders = useQuery({
    queryKey: ["claim-folders"],
    queryFn: () => api.get<{ items: FolderDTO[] }>("/claim-folders"),
  });

  const tasks = useQuery({
    queryKey: ["my-tasks", tab],
    queryFn: () => api.get<Page<ImageDTO>>(`/claims/my?status=${tab}&page_size=100`),
  });

  const counts = useQuery({
    queryKey: ["my-counts"],
    queryFn: async () => {
      const [a, r, s] = await Promise.all([
        api.get<Page<ImageDTO>>("/claims/my?status=active&page_size=1"),
        api.get<Page<ImageDTO>>("/claims/my?status=rejected&page_size=1"),
        api.get<Page<ImageDTO>>("/claims/my?status=submitted&page_size=1"),
      ]);
      return { active: a.total, rejected: r.total, submitted: s.total };
    },
  });

  const claim = useMutation({
    mutationFn: (body: unknown) => api.post<ClaimResult>("/claims", body),
    onSuccess: (res) => {
      if (res.granted === 0) {
        message.warning("没有可领取的图片了（可能已被其他人领走）");
      } else if (res.hold && res.hold.current >= res.hold.limit) {
        // 整包领取也会被持有上限截断，这里说清楚还剩多少在池里
        message.warning(
          `已领取 ${res.granted} 张，已达持有上限 ${res.hold.limit} 张` +
            (res.remaining_in_pool > 0 ? `，文件夹里还剩 ${res.remaining_in_pool} 张，完成后再领` : ""),
        );
        setTab("active");
        if (res.images.length > 0) nav(`/workbench/${res.images[0].id}`);
      } else {
        message.success(`已领取 ${res.granted} 张`);
        setTab("active");
        if (res.images.length > 0) nav(`/workbench/${res.images[0].id}`);
      }
      qc.invalidateQueries({ queryKey: ["my-tasks"] });
      qc.invalidateQueries({ queryKey: ["my-counts"] });
      qc.invalidateQueries({ queryKey: ["claim-folders"] });
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "领取失败"),
  });

  const release = useMutation({
    mutationFn: (ids: number[]) => api.post("/claims/release", { image_ids: ids }),
    onSuccess: () => {
      message.success("已放弃");
      qc.invalidateQueries({ queryKey: ["my-tasks"] });
      qc.invalidateQueries({ queryKey: ["my-counts"] });
      qc.invalidateQueries({ queryKey: ["claim-folders"] });
    },
    onError: (e) => message.error(e instanceof ApiError ? e.message : "操作失败"),
  });

  function openTask(row: ImageDTO) {
    if (row.status === "rejected" || row.status === "assigned") nav(`/workbench/${row.id}`);
    else nav(`/workbench/${row.id}`);
  }

  const columns = [
    {
      title: "图片",
      dataIndex: "thumb_url",
      width: 80,
      render: (_: string, row: ImageDTO) => (
        <img className="thumb" src={row.thumb_url} alt={row.stored_name} onClick={() => openTask(row)} />
      ),
    },
    { title: "文件名", dataIndex: "stored_name", render: (v: string, row: ImageDTO) => <a onClick={() => openTask(row)}>{v}</a> },
    { title: "文件夹", dataIndex: "folder" },
    {
      title: "分数",
      width: 120,
      render: (_: unknown, row: ImageDTO) =>
        row.total === null ? <Tag>未评分</Tag> : (
          <Space size={4}>
            <b>{row.total}</b>
            <Tag color={row.total >= 90 ? "green" : row.total >= 60 ? "blue" : "volcano"}>{row.grade}</Tag>
          </Space>
        ),
    },
    {
      title: "状态",
      dataIndex: "status",
      width: 110,
      render: (v: string, row: ImageDTO) => (
        <Space size={4}>
          <Tag color={STATUS_COLOR[v]}>{STATUS_LABEL[v] || v}</Tag>
          {row.reject_count > 0 && <Badge count={row.reject_count} title="被驳回次数" />}
        </Space>
      ),
    },
    {
      title: "点评/异常",
      dataIndex: "comment",
      ellipsis: true,
      render: (v: string, row: ImageDTO) =>
        row.kind === "exception" ? <Tag color="orange">异常：{row.exception_reasons?.length ?? 0} 项</Tag> : v || "-",
    },
    {
      title: "操作",
      width: 180,
      render: (_: unknown, row: ImageDTO) => (
        <Space>
          <Button size="small" type="primary" onClick={() => openTask(row)}>
            {row.status === "assigned" ? "继续标注" : row.status === "rejected" ? "按批注重做" : "查看"}
          </Button>
          {row.status === "assigned" && (
            <Button size="small" danger onClick={() => release.mutate([row.id])}>
              放弃
            </Button>
          )}
        </Space>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Row gutter={14}>
        <Col span={10}>
          <Card size="small" title="按张领取" styles={{ body: { paddingTop: 12 } }}>
            <Space>
              <span>领取张数</span>
              <Input
                type="number"
                min={1}
                max={me?.limits.max_claim_count ?? 100}
                value={count}
                onChange={(e) => setCount(Math.max(1, Number(e.target.value) || 1))}
                style={{ width: 100 }}
                data-test="claim-count"
              />
              <Button
                type="primary"
                loading={claim.isPending}
                onClick={() => claim.mutate({ mode: "count", count })}
                data-test="claim-by-count"
              >
                领取
              </Button>
            </Space>
            <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}>
              当前持有 {counts.data?.active ?? 0} / {me?.limits.max_hold_per_user ?? 100} 张；
              领取有效期 {me?.limits.lock_ttl_hours ?? 24} 小时，超时未提交会自动回收。
            </Typography.Paragraph>
          </Card>
        </Col>
        <Col span={14}>
          <Card size="small" title="按文件夹领取" styles={{ body: { paddingTop: 12 } }} loading={folders.isLoading}>
            {folders.data?.items?.length ? (
              <Row gutter={[10, 10]}>
                {folders.data.items.slice(0, 6).map((f) => (
                  <Col span={12} key={f.id}>
                    <Card size="small" styles={{ body: { padding: 10 } }}>
                      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                        <b>{f.name}</b>
                        <Button
                          size="small"
                          type="primary"
                          ghost
                          loading={claim.isPending}
                          onClick={() =>
                            claim.mutate({ mode: "folder", folder_id: f.id })
                          }
                        >
                          领取剩余 {f.unassigned}
                        </Button>
                      </div>
                      <Progress
                        percent={Math.round(((f.total - f.unassigned) / Math.max(1, f.total)) * 100)}
                        size="small"
                        format={() => `${f.total - f.unassigned}/${f.total}`}
                      />
                    </Card>
                  </Col>
                ))}
              </Row>
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无可领取的文件夹" />
            )}
          </Card>
        </Col>
      </Row>

      {(counts.data?.rejected ?? 0) > 0 && (
        <Alert
          type="error"
          showIcon
          title={`你有 ${counts.data?.rejected} 条被驳回的任务，请按审核批注修改后重新提交`}
          action={
            <Button size="small" onClick={() => setTab("rejected")}>
              去处理
            </Button>
          }
        />
      )}

      <Card
        size="small"
        styles={{ body: { paddingTop: 6 } }}
        title={
          <Segmented
            value={tab}
            onChange={(v) => setTab(v as string)}
            options={TABS.map((t) => ({
              ...t,
              label: (
                <span>
                  {t.label}
                  {t.value === "active" && counts.data?.active ? <Badge count={counts.data.active} style={{ marginLeft: 6 }} /> : null}
                  {t.value === "rejected" && counts.data?.rejected ? <Badge count={counts.data.rejected} style={{ marginLeft: 6 }} /> : null}
                </span>
              ),
            }))}
          />
        }
      >
        <Table
          rowKey="id"
          size="middle"
          loading={tasks.isLoading}
          dataSource={tasks.data?.items ?? []}
          columns={columns as never}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="这个列表还没有任务" /> }}
        />
      </Card>
    </Space>
  );
}
