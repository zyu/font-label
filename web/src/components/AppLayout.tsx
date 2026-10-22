import { useMemo } from "react";
import { Layout, Menu, Avatar, Dropdown, Space, Tag, Typography } from "antd";
import {
  EditOutlined,
  AuditOutlined,
  BarChartOutlined,
  CloudUploadOutlined,
  FolderOpenOutlined,
  TeamOutlined,
  DownloadOutlined,
  SettingOutlined,
  LogoutOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";

const ROLE_LABEL: Record<string, string> = { admin: "管理员", reviewer: "审核员", annotator: "标注员" };

export default function AppLayout() {
  const { user, logout } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();

  const items = useMemo(() => {
    const role = user?.role;
    const list: { key: string; icon: React.ReactNode; label: string }[] = [];
    if (role === "annotator" || role === "admin") {
      list.push({ key: "/workbench", icon: <EditOutlined />, label: "标注工作台" });
    }
    if (role === "reviewer" || role === "admin") {
      list.push({ key: "/review", icon: <AuditOutlined />, label: "审核队列" });
    }
    list.push({ key: "/stats", icon: <BarChartOutlined />, label: role === "annotator" ? "我的统计" : "统计看板" });
    if (role === "admin") {
      list.push(
        { key: "/admin/upload", icon: <CloudUploadOutlined />, label: "上传图片" },
        { key: "/admin/folders", icon: <FolderOpenOutlined />, label: "文件夹" },
        { key: "/admin/users", icon: <TeamOutlined />, label: "用户管理" },
        { key: "/admin/exports", icon: <DownloadOutlined />, label: "导出结果" },
        { key: "/admin/settings", icon: <SettingOutlined />, label: "评分与设置" },
      );
    }
    return list;
  }, [user?.role]);

  const selected = items.find((i) => loc.pathname.startsWith(i.key))?.key ?? "";

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Layout.Sider width={216} theme="light" style={{ borderRight: "1px solid #eef0f4" }}>
        <div className="brand">
          <div className="brand-mark">字</div>
          <div>
            <div className="brand-title">书法评分标注</div>
            <div className="brand-sub">font-label</div>
          </div>
        </div>
        <Menu
          mode="inline"
          selectedKeys={[selected]}
          items={items}
          onClick={(e) => nav(e.key)}
          style={{ borderInlineEnd: "none" }}
        />
      </Layout.Sider>
      <Layout>
        <Layout.Header className="app-header">
          <Typography.Text type="secondary">
            {user?.role === "annotator"
              ? "领取任务 → 左图右评分 → 提交审核；被打回的任务在「标注工作台 · 已驳回」"
              : user?.role === "reviewer"
                ? "逐条审核：通过或填写批注打回，打回任务会回到原标注员"
                : "管理员：上传 → 分配 → 审核 → 统计 → 导出"}
          </Typography.Text>
          <Dropdown
            menu={{
              items: [{ key: "logout", icon: <LogoutOutlined />, label: "退出登录" }],
              onClick: () => {
                logout();
                nav("/login");
              },
            }}
          >
            <Space style={{ cursor: "pointer" }}>
              <Avatar size={30} icon={<UserOutlined />} style={{ background: "#2b5cff" }} />
              <span>{user?.display_name}</span>
              <Tag color="blue">{ROLE_LABEL[user?.role || ""]}</Tag>
            </Space>
          </Dropdown>
        </Layout.Header>
        <Layout.Content className="app-content">
          <Outlet />
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
