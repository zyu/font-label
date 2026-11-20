import { useEffect, useState } from "react";
import {
  Button,
  Card,
  Col,
  Divider,
  Input,
  InputNumber,
  Row,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import type { Rubric } from "../../api/types";
import { useAuth } from "../../auth";

const NUM_KEYS: { key: string; label: string; hint: string; min: number; max: number }[] = [
  { key: "lock_ttl_hours", label: "领取有效期（小时）", hint: "超时未提交自动回收（1~8760 小时）", min: 1, max: 8760 },
  { key: "max_hold_per_user", label: "每人未提交上限", hint: "防止囤任务（1~10000 张）", min: 1, max: 10000 },
  { key: "claim_default_count", label: "按张领取默认数量", hint: "1~10000 张", min: 1, max: 10000 },
  { key: "max_claim_count", label: "单次领取上限", hint: "1~10000 张", min: 1, max: 10000 },
  { key: "max_file_mb", label: "单张图片大小上限（MB）", hint: "上传时校验（0~1024 MB）", min: 0, max: 1024 },
];

export default function SettingsPage() {
  const { reload } = useAuth();
  const [settings, setSettings] = useState<Record<string, unknown>>({});
  const [templates, setTemplates] = useState("");
  const [reasons, setReasons] = useState("");
  const [commentMin, setCommentMin] = useState(10);
  const [commentMax, setCommentMax] = useState(50);
  const [saving, setSaving] = useState(false);

  const rubric = useQuery({ queryKey: ["rubric"], queryFn: () => api.get<Rubric>("/rubric") });
  const allSettings = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<{ settings: Record<string, unknown> }>("/settings"),
  });

  useEffect(() => {
    if (!rubric.data) return;
    setCommentMin(rubric.data.dim_comment_min ?? 10);
    setCommentMax(rubric.data.dim_comment_max ?? 50);
  }, [rubric.data]);

  useEffect(() => {
    if (!allSettings.data) return;
    setSettings(allSettings.data.settings);
    setTemplates(JSON.stringify(allSettings.data.settings.comment_templates ?? [], null, 2));
    setReasons(JSON.stringify(allSettings.data.settings.exception_reasons ?? [], null, 2));
  }, [allSettings.data]);

  async function putSetting(key: string, value: unknown) {
    try {
      await api.put(`/settings/${key}`, value);
      message.success("已保存");
      allSettings.refetch();
      reload();
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : "保存失败");
    }
  }

  function saveJsonSetting(key: string, raw: string) {
    let value: unknown;
    try {
      value = JSON.parse(raw);
    } catch {
      message.error("JSON 格式错误");
      return;
    }
    return putSetting(key, value);
  }

  async function saveRubric() {
    if (!rubric.data) return;
    setSaving(true);
    try {
      const body = {
        name: `${rubric.data.name.replace(/（.*）$/, "")}（自定义 ${new Date().toLocaleDateString("zh-CN")}）`,
        dims: rubric.data.dims,
        dim_comment_min: commentMin,
        dim_comment_max: commentMax,
      };
      const created = await api.post<{ id: number }>("/rubrics", body);
      await api.post(`/rubrics/${created.id}/activate`, {});
      message.success("已保存并启用新的评级方案（历史数据保留旧快照）");
      rubric.refetch();
      reload();
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Space direction="vertical" size={14} style={{ width: "100%" }}>
      <Card
        size="small"
        title="五维评级方案"
        extra={
          <Button type="primary" loading={saving} onClick={saveRubric}>
            保存为新方案并启用
          </Button>
        }
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          每个维度独立选择 S、A、B、C 或 D，并填写对应评语。
        </Typography.Paragraph>
        <Table
          size="small"
          pagination={false}
          rowKey="key"
          dataSource={rubric.data?.dims ?? []}
          columns={[
            { title: "维度", dataIndex: "label", width: 120, render: (v: string) => <Tag color="blue">{v}</Tag> },
            { title: "标识", dataIndex: "key", width: 140 },
            { title: "可选等级", render: () => "S / A / B / C / D" },
          ]}
        />
        <Space style={{ marginTop: 12 }} wrap>
          <span>每维评语字数下限</span>
          <InputNumber min={1} max={200} value={commentMin} onChange={(v) => setCommentMin(Number(v ?? 10))} />
          <span>上限</span>
          <InputNumber min={10} max={500} value={commentMax} onChange={(v) => setCommentMax(Number(v ?? 50))} />
        </Space>
      </Card>

      <Row gutter={14}>
        <Col span={12}>
          <Card size="small" title="常用评语（JSON 数组）" extra={<a onClick={() => saveJsonSetting("comment_templates", templates)}>保存</a>}>
            <Typography.Paragraph type="secondary">
              使用 <code>{'{"dimension":"white","grade":"S","text":"笔画间距均匀，字内空白分布协调。"}'}</code> 按维度和等级配置。自定义旧评语需补充维度和等级后才会出现在对应卡片。
            </Typography.Paragraph>
            <Input.TextArea rows={8} value={templates} onChange={(e) => setTemplates(e.target.value)} />
          </Card>
        </Col>
        <Col span={12}>
          <Card size="small" title="异常原因（JSON 数组，key + label）" extra={<a onClick={() => saveJsonSetting("exception_reasons", reasons)}>保存</a>}>
            <Input.TextArea rows={8} value={reasons} onChange={(e) => setReasons(e.target.value)} />
          </Card>
        </Col>
      </Row>

      <Card size="small" title="系统参数">
        <Row gutter={[16, 12]}>
          {NUM_KEYS.map((k) => (
            <Col span={8} key={k.key}>
              <div style={{ marginBottom: 4 }}>{k.label}</div>
              <Space>
                <InputNumber
                  min={k.min}
                  max={k.max}
                  precision={0}
                  value={Number(settings[k.key] ?? 0)}
                  onChange={(v) => setSettings((prev) => ({ ...prev, [k.key]: Number(v ?? 0) }))}
                />
                <Button size="small" onClick={() => putSetting(k.key, Number(settings[k.key] ?? 0))}>
                  保存
                </Button>
              </Space>
              {k.hint && (
                <div style={{ fontSize: 11, color: "#8b95a8", marginTop: 2 }}>{k.hint}</div>
              )}
            </Col>
          ))}
        </Row>
        <Divider style={{ margin: "14px 0" }} />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          修改参数后立即生效（领取上限、超时回收时长在下次领取/回收任务时读取）。
        </Typography.Text>
      </Card>
    </Space>
  );
}
