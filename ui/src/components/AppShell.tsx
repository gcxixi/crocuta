import React, { useState } from "react"
import {
  Button,
  Empty,
  Flex,
  Form,
  Input,
  Layout,
  Menu,
  Modal,
  Result,
  Select,
  Space,
  Tag,
  Typography,
  message,
} from "antd"
import {
  BugOutlined,
  CodeOutlined,
  FileSearchOutlined,
  FundProjectionScreenOutlined,
  HomeOutlined,
  PlusOutlined,
  SettingOutlined,
  SwapOutlined,
} from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useParams,
} from "react-router-dom"
import { api, type Project } from "../api"
import { ErrorView, Loading } from "./Common"
import { IssuesPage } from "../pages/IssuesPage"
import { IssueDetailPage } from "../pages/IssueDetailPage"
import { ReleasesPage } from "../pages/ReleasesPage"
import { SignalsPage } from "../pages/SignalsPage"
import { ReportsPage } from "../pages/ReportsPage"
import { SettingsPage } from "../pages/SettingsPage"

export function AppShell() {
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [collapsed, setCollapsed] = useState(false)
  const [openCreateProject, setOpenCreateProject] = useState(false)
  const [form] = Form.useForm<{ name: string; slug: string; platform: string }>()

  const org = "default"
  const projectsQuery = useQuery({
    queryKey: ["projects", org],
    queryFn: () => api.projects(org),
  })

  const projectMatch = location.pathname.match(/^\/projects\/([^\/]+)/)
  const projectID = projectMatch ? decodeURIComponent(projectMatch[1]) : ""
  const projectList = projectsQuery.data ?? []
  const currentProject = projectList.find((item) => item.id === projectID || item.slug === projectID)
  const selectedProject = projectID || projectList[0]?.id || ""

  const createProjectMutation = useMutation({
    mutationFn: (vals: { name: string; slug: string; platform: string }) =>
      api.createProject(org, vals),
    onSuccess: (newProj) => {
      setOpenCreateProject(false)
      form.resetFields()
      message.success(`项目 ${newProj.name} 创建成功`)
      void queryClient.invalidateQueries({ queryKey: ["projects", org] })
      navigate(`/projects/${encodeURIComponent(newProj.id)}/issues`)
    },
    onError: (err: Error) => {
      message.error(`创建项目失败: ${err.message}`)
    },
  })

  const navItems = [
    {
      key: "issues",
      icon: <BugOutlined />,
      label: "Issues",
      path: `/projects/${encodeURIComponent(selectedProject)}/issues`,
    },
    {
      key: "releases",
      icon: <CodeOutlined />,
      label: "Releases",
      path: `/projects/${encodeURIComponent(selectedProject)}/releases`,
    },
    {
      key: "signals",
      icon: <FundProjectionScreenOutlined />,
      label: "Signals",
      path: `/projects/${encodeURIComponent(selectedProject)}/signals`,
    },
    {
      key: "reports",
      icon: <FileSearchOutlined />,
      label: "Client Reports",
      path: `/projects/${encodeURIComponent(selectedProject)}/client-reports`,
    },
    {
      key: "settings",
      icon: <SettingOutlined />,
      label: "Settings",
      path: `/projects/${encodeURIComponent(selectedProject)}/settings`,
    },
  ]

  const activeKey = navItems.find((item) => location.pathname.includes(`/${item.key}`))?.key ?? "issues"

  if (projectsQuery.isLoading) return <Loading tip="正在连接 SentryX 控制台..." />
  if (projectsQuery.isError) {
    return (
      <main className="center-page">
        <ErrorView error={projectsQuery.error as Error} retry={() => void projectsQuery.refetch()} />
      </main>
    )
  }

  if (projectList.length === 0) {
    return (
      <main className="center-page">
        <Empty
          description="当前工作空间还没有项目，请先创建第一个项目。"
          style={{ marginBottom: 16 }}
        >
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpenCreateProject(true)}>
            新建项目
          </Button>
        </Empty>
      </main>
    )
  }

  return (
    <Layout className="app-layout">
      <Layout.Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        theme="dark"
        className="app-sider"
        width={230}
      >
        <div className="brand">
          <div className="brand-icon">
            <SwapOutlined />
          </div>
          {!collapsed && (
            <Flex align="center">
              <span>SentryX</span>
              <span className="brand-badge">PRO</span>
            </Flex>
          )}
        </div>

        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[activeKey]}
          items={navItems.map((item) => ({
            ...item,
            onClick: () => navigate(item.path),
          }))}
          style={{ background: "transparent", borderRight: 0, marginTop: 12 }}
        />
      </Layout.Sider>

      <Layout className="app-main-layout">
        <Layout.Header className="app-header">
          <Flex justify="space-between" align="center" style={{ width: "100%" }}>
            <Space size="middle">
              <HomeOutlined style={{ color: "#6366f1", fontSize: 16 }} />
              <Typography.Text strong style={{ fontSize: 15 }}>
                {currentProject?.name ?? selectedProject}
              </Typography.Text>
              <Tag color="indigo">{currentProject?.platform || "javascript"}</Tag>
            </Space>

            <Space size="middle">
              <Select
                aria-label="project"
                value={selectedProject}
                style={{ minWidth: 240 }}
                options={projectList.map((item) => ({
                  value: item.id,
                  label: `${item.name} (${item.slug})`,
                }))}
                onChange={(value) => navigate(`/projects/${encodeURIComponent(value)}/issues`)}
              />
              <Button
                icon={<PlusOutlined />}
                onClick={() => setOpenCreateProject(true)}
              >
                新建项目
              </Button>
            </Space>
          </Flex>
        </Layout.Header>

        <Layout.Content className="app-content">
          <Routes>
            <Route path="/projects/:projectId/issues" element={<IssuesPage />} />
            <Route path="/projects/:projectId/issues/:issueId" element={<IssueDetailPage />} />
            <Route path="/projects/:projectId/releases" element={<ReleasesPage />} />
            <Route path="/projects/:projectId/signals" element={<SignalsPage />} />
            <Route path="/projects/:projectId/client-reports" element={<ReportsPage />} />
            <Route path="/projects/:projectId/settings" element={<SettingsPage />} />
            <Route
              path="/"
              element={<Navigate replace to={`/projects/${encodeURIComponent(selectedProject)}/issues`} />}
            />
            <Route
              path="*"
              element={<Result status="404" title="页面不存在" subTitle="请确认 URL 路由是否正确。" />}
            />
          </Routes>
        </Layout.Content>
      </Layout>

      {/* Create Project Modal */}
      <Modal
        title="新建监控项目"
        open={openCreateProject}
        confirmLoading={createProjectMutation.isPending}
        onCancel={() => setOpenCreateProject(false)}
        onOk={() => {
          form.validateFields().then((vals) => {
            createProjectMutation.mutate({
              name: vals.name.trim(),
              slug: (vals.slug || vals.name).toLowerCase().replace(/[^a-z0-9_-]/g, "-"),
              platform: vals.platform || "javascript",
            })
          })
        }}
      >
        <Form form={form} layout="vertical" initialValues={{ platform: "javascript" }}>
          <Form.Item
            name="name"
            label="项目名称"
            rules={[{ required: true, message: "请输入项目名称" }]}
          >
            <Input placeholder="例如: Web 官网前端" />
          </Form.Item>
          <Form.Item name="slug" label="项目 Slug (唯一标识)">
            <Input placeholder="例如: web-frontend (留空自动生成)" />
          </Form.Item>
          <Form.Item name="platform" label="技术平台">
            <Select
              options={[
                { label: "JavaScript / TypeScript", value: "javascript" },
                { label: "React", value: "react" },
                { label: "Vue.js", value: "vue" },
                { label: "Node.js", value: "node" },
                { label: "Angular", value: "angular" },
                { label: "Go", value: "go" },
                { label: "Python", value: "python" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </Layout>
  )
}
