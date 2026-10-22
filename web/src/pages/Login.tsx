import { useEffect, useState } from "react";
import { Button, Card, Form, Input, Modal, Typography, message, Alert } from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useAuth } from "../auth";
import { api, ApiError } from "../api/client";

export default function LoginPage() {
  const { user, login, reload } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const [loading, setLoading] = useState(false);
  const [pwdOpen, setPwdOpen] = useState(false);
  const [pwdLoading, setPwdLoading] = useState(false);
  const [form] = Form.useForm();
  const [pwdForm] = Form.useForm();

  useEffect(() => {
    if (user) {
      setPwdOpen(true);
    }
    if (params.get("change") === "1") setPwdOpen(true);
  }, [user, params]);

  async function onFinish(values: { username: string; password: string }) {
    setLoading(true);
    try {
      const me = await login(values.username, values.password);
      if (me.user.must_change_pwd) {
        setPwdOpen(true);
      } else {
        nav("/", { replace: true });
      }
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : "登录失败");
    } finally {
      setLoading(false);
    }
  }

  async function changePassword(values: { old_password: string; new_password: string; confirm: string }) {
    if (values.new_password !== values.confirm) {
      message.error("两次输入的新密码不一致");
      return;
    }
    setPwdLoading(true);
    try {
      const res = await api.post<{ access_token: string }>("/auth/password", {
        old_password: values.old_password,
        new_password: values.new_password,
      });
      if (res?.access_token) localStorage.setItem("fl_token", res.access_token);
      await reload();
      message.success("密码已更新");
      setPwdOpen(false);
      nav("/", { replace: true });
    } catch (e) {
      message.error(e instanceof ApiError ? e.message : "修改失败");
    } finally {
      setPwdLoading(false);
    }
  }

  return (
    <div className="login-wrap">
      <div className="login-hero">
        <h1>硬笔书法作业评分标注平台</h1>
        <p>
          上传学生作业照片，按张或按文件夹领取任务，五维（白平衡 / 黑平衡 / 参差 / 结构 / 控笔，各 20 分）打分并写点评；
          审核员逐条复核，可批注打回；全程留痕、可统计、可导出。
        </p>
        <div className="quote">
          「白平衡要匀，黑平衡要稳；四面参差有长短，结构随字定占位，控笔见轻重。」
        </div>
      </div>
      <div className="login-panel">
        <div className="login-card">
          <h2>登录</h2>
          <div className="hint">账号由管理员创建，如忘记密码请联系管理员重置</div>
          <Form form={form} layout="vertical" onFinish={onFinish} requiredMark={false}>
            <Form.Item name="username" label="账号" rules={[{ required: true, message: "请输入账号" }]}>
              <Input size="large" placeholder="请输入账号" autoComplete="username" />
            </Form.Item>
            <Form.Item name="password" label="密码" rules={[{ required: true, message: "请输入密码" }]}>
              <Input.Password size="large" placeholder="请输入密码" autoComplete="current-password" />
            </Form.Item>
            <Button type="primary" size="large" htmlType="submit" block loading={loading}>
              登录
            </Button>
          </Form>
          <Typography.Paragraph type="secondary" style={{ marginTop: 18, fontSize: 12 }}>
            首次登录会要求修改初始密码。
          </Typography.Paragraph>
        </div>
      </div>

      <Modal
        open={pwdOpen}
        title="修改初始密码"
        maskClosable={false}
        closable={false}
        footer={null}
        destroyOnHidden
      >
        <Alert
          type="warning"
          showIcon
          title="首次登录必须修改密码后才能使用系统"
          style={{ marginBottom: 14 }}
        />
        <Form form={pwdForm} layout="vertical" onFinish={changePassword} requiredMark={false}>
          <Form.Item name="old_password" label="原密码" rules={[{ required: true, message: "请输入原密码" }]}>
            <Input.Password />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[{ required: true, min: 8, message: "至少 8 位，含字母和数字" }]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item name="confirm" label="确认新密码" rules={[{ required: true, message: "请再次输入" }]}>
            <Input.Password />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={pwdLoading}>
            保存并进入系统
          </Button>
        </Form>
      </Modal>
    </div>
  );
}
