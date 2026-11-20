import { useMemo, useState } from "react";
import { Layout, Menu, Avatar, Button, Drawer, Dropdown, Space, Tag, Typography } from "antd";
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
  SafetyCertificateOutlined,
  FileSearchOutlined,
  MenuOutlined,
} from "@ant-design/icons";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";

const ROLE_LABEL: Record<string, string> = { admin: "管理员", reviewer: "审核员", annotator: "标注员", quality: "质检仲裁员" };

export default function AppLayout() {
  const { user, logout } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);

  const items = useMemo(() => {
    const role = user?.role;
    const list: { key: string; icon: React.ReactNode; label: string }[] = [];
    if (role === "annotator" || role === "admin") {
      list.push({ key: "/workbench", icon: <EditOutlined />, label: "标注工作台" });
    }
    if (role === "reviewer" || role === "admin") {
      list.push({ key: "/review", icon: <AuditOutlined />, label: "审核队列" });
    }
    if (role === "admin" || role === "quality") {
      list.push({ key: "/quality", icon: <SafetyCertificateOutlined />, label: "质检与仲裁" });
    }
    if (role !== "quality") list.push({ key: "/stats", icon: <BarChartOutlined />, label: role === "annotator" ? "我的统计" : "统计看板" });
    if (role === "admin") {
      list.push(
        { key: "/admin/upload", icon: <CloudUploadOutlined />, label: "上传图片" },
        { key: "/admin/folders", icon: <FolderOpenOutlined />, label: "文件夹" },
        { key: "/admin/users", icon: <UserOutlined />, label: "用户管理" },
        { key: "/admin/groups", icon: <TeamOutlined />, label: "分组与任务下发" },
        { key: "/admin/audit", icon: <FileSearchOutlined />, label: "操作日志" },
        { key: "/admin/exports", icon: <DownloadOutlined />, label: "导出结果" },
        { key: "/admin/settings", icon: <SettingOutlined />, label: "评级与设置" },
      );
    }
    return list;
  }, [user?.role]);

  const selected = items.find((i) => loc.pathname.startsWith(i.key))?.key ?? "";

  const navigation = (
    <Menu
      mode="inline"
      selectedKeys={[selected]}
      items={items}
      onClick={({ key }) => {
        setMenuOpen(false);
        nav(key);
      }}
      style={{ borderInlineEnd: "none" }}
    />
  );

  return (
    <Layout className="app-shell">
      <Layout.Sider className="app-sider" width={216} theme="light" style={{ borderRight: "1px solid #eef0f4" }}>
        <div className="brand">
          <div className="brand-mark">字</div>
          <div>
            <div className="brand-title">书法评级标注</div>
            <div className="brand-sub">font-label</div>
          </div>
        </div>
        {navigation}
      </Layout.Sider>
      <Layout className="app-main">
        <Layout.Header className="app-header">
          <div className="app-mobile-nav">
            <Button
              type="text"
              icon={<MenuOutlined />}
              aria-label="打开导航菜单"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen(true)}
            />
            <span className="brand-title">书法评级标注</span>
          </div>
          <Typography.Text className="app-header-hint" type="secondary">
            {user?.role === "annotator"
              ? "在工作台完成标注并提交审核"
              : user?.role === "reviewer"
                ? "逐条审核：通过或填写批注打回，打回任务会回到原标注员"
                : user?.role === "quality"
                  ? "处理分配给你的抽检和争议仲裁"
                  : "管理员：上传 → 分配 → 统计 → 导出"}
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
            <Space className="app-account" style={{ cursor: "pointer" }}>
              <Avatar size={30} icon={<UserOutlined />} style={{ background: "#2b5cff" }} />
              <span className="app-account-name">{user?.display_name}</span>
              <Tag className="app-account-role" color="blue">{ROLE_LABEL[user?.role || ""]}</Tag>
            </Space>
          </Dropdown>
        </Layout.Header>
        <Drawer title="导航菜单" placement="left" size="min(280px, 90vw)" open={menuOpen}
          onClose={() => setMenuOpen(false)} styles={{ body: { padding: 0 } }}>
          {navigation}
        </Drawer>
        <Layout.Content className="app-content">
          <Outlet />
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
