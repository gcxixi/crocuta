import React, { useEffect, useMemo, useState } from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntApp, Alert, Breadcrumb, Button, Card, ConfigProvider, Empty, Flex, Form, Input, Layout, Menu, Modal, Result, Select, Segmented, Space, Spin, Statistic, Table, Tag, Tabs, Timeline, Typography, theme } from 'antd'
import { BugOutlined, CodeOutlined, FileSearchOutlined, FundProjectionScreenOutlined, HomeOutlined, SettingOutlined, SwapOutlined, ReloadOutlined } from '@ant-design/icons'
import { QueryClient, QueryClientProvider, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BrowserRouter, Link, Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import type { ColumnsType } from 'antd/es/table'
import { api, type ClientReport, type Event, type Frame, type Issue, type Project, type Release, type Signal } from './api'
import './styles.css'

const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 10_000, retry: 1 } } })

function formatTime(value?: string) { return value ? new Date(value).toLocaleString() : '—' }
function levelColor(level?: string) { return level === 'fatal' ? 'magenta' : level === 'error' ? 'red' : level === 'warning' ? 'orange' : 'blue' }
function ErrorView({ error, retry }: { error: Error; retry?: () => void }) { return <Alert type="error" showIcon message="请求失败" description={error.message} action={retry ? <Button size="small" onClick={retry}>重试</Button> : undefined} /> }
function Loading() { return <Flex justify="center" align="center" className="loading"><Spin /></Flex> }
function JsonView({ value }: { value: unknown }) { return <pre className="json-view">{JSON.stringify(value ?? {}, null, 2)}</pre> }

function AppShell() {
  const location = useLocation()
  const navigate = useNavigate()
  const [collapsed, setCollapsed] = useState(false)
  const org = 'default'
  const projectsQuery = useQuery({ queryKey: ['projects', org], queryFn: () => api.projects(org) })
  const projectMatch = location.pathname.match(/^\/projects\/([^/]+)/)
  const projectID = projectMatch ? decodeURIComponent(projectMatch[1]) : ''
  const project = projectsQuery.data?.find((item) => item.id === projectID || item.slug === projectID)
  const selectedProject = projectID || projectsQuery.data?.[0]?.id || ''
  const items = [
    { key: 'issues', icon: <BugOutlined />, label: 'Issues', path: `/projects/${encodeURIComponent(selectedProject)}/issues` },
    { key: 'releases', icon: <CodeOutlined />, label: 'Releases', path: `/projects/${encodeURIComponent(selectedProject)}/releases` },
    { key: 'signals', icon: <FundProjectionScreenOutlined />, label: 'Signals', path: `/projects/${encodeURIComponent(selectedProject)}/signals` },
    { key: 'reports', icon: <FileSearchOutlined />, label: 'Client Reports', path: `/projects/${encodeURIComponent(selectedProject)}/client-reports` },
    { key: 'settings', icon: <SettingOutlined />, label: 'Project Settings', path: `/projects/${encodeURIComponent(selectedProject)}/settings` },
  ]
  const active = items.find((item) => location.pathname.includes(`/${item.key}`))?.key ?? 'issues'
  if (projectsQuery.isLoading) return <Loading />
  if (projectsQuery.isError) return <main className="center-page"><ErrorView error={projectsQuery.error as Error} retry={() => void projectsQuery.refetch()} /></main>
  if (!selectedProject) return <main className="center-page"><Empty description="还没有项目，请先通过控制面 API 创建项目" /></main>
  return <Layout className="app-layout">
    <Layout.Sider collapsible collapsed={collapsed} onCollapse={setCollapsed} theme="dark">
      <div className="brand"><SwapOutlined />{!collapsed && <span>SentryX</span>}</div>
      <Menu theme="dark" mode="inline" selectedKeys={[active]} items={items.map((item) => ({ ...item, onClick: () => navigate(item.path) }))} />
    </Layout.Sider>
    <Layout>
      <Layout.Header className="app-header">
        <Flex justify="space-between" align="center" gap={16}>
          <Space><HomeOutlined /><Typography.Text strong>{project?.name ?? selectedProject}</Typography.Text><Tag>{project?.platform || 'javascript'}</Tag></Space>
          <Select aria-label="project" value={selectedProject} style={{ minWidth: 220 }} options={(projectsQuery.data ?? []).map((item) => ({ value: item.id, label: `${item.name} (${item.slug})` }))} onChange={(value) => navigate(`/projects/${encodeURIComponent(value)}/issues`)} />
        </Flex>
      </Layout.Header>
      <Layout.Content className="app-content"><Routes>
        <Route path="/projects/:projectId/issues" element={<IssuesPage />} />
        <Route path="/projects/:projectId/issues/:issueId" element={<IssueDetailPage />} />
        <Route path="/projects/:projectId/releases" element={<ReleasesPage />} />
        <Route path="/projects/:projectId/signals" element={<SignalsPage />} />
        <Route path="/projects/:projectId/client-reports" element={<ReportsPage />} />
        <Route path="/projects/:projectId/settings" element={<SettingsPage />} />
        <Route path="/" element={<Navigate replace to={`/projects/${encodeURIComponent(selectedProject)}/issues`} />} />
        <Route path="*" element={<Result status="404" title="页面不存在" />} />
      </Routes></Layout.Content>
    </Layout>
  </Layout>
}

function PageHeader({ title, extra }: { title: string; extra?: React.ReactNode }) { return <Flex justify="space-between" align="center" className="page-header"><div><Breadcrumb items={[{ title: 'SentryX' }, { title }]} /><Typography.Title level={2}>{title}</Typography.Title></div>{extra}</Flex> }

function IssuesPage() {
  const { projectId = '' } = useParams()
  const navigate = useNavigate()
  const query = useQuery({ queryKey: ['issues', projectId], queryFn: () => api.issues(projectId) })
  const [search, setSearch] = useState('')
  const [level, setLevel] = useState('all')
  if (query.isLoading) return <Loading />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />
  const issues = (query.data ?? []).filter((item) => (!search || item.title.toLowerCase().includes(search.toLowerCase())) && (level === 'all' || item.level === level))
  const columns: ColumnsType<Issue> = [
    { title: 'Issue', dataIndex: 'title', render: (value: string, row) => <Link to={`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`}><Typography.Text strong ellipsis={{ tooltip: value }}>{value}</Typography.Text></Link> },
    { title: 'Level', dataIndex: 'level', width: 110, render: (value?: string) => <Tag color={levelColor(value)}>{value || 'error'}</Tag> },
    { title: 'Events', dataIndex: 'count', sorter: (a, b) => a.count - b.count, width: 110 },
    { title: 'First seen', dataIndex: 'first_seen', render: formatTime },
    { title: 'Last seen', dataIndex: 'last_seen', render: formatTime },
  ]
  return <><PageHeader title="Issues" extra={<Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新</Button>} /><Card>
    <Flex gap={12} wrap="wrap" className="filter-bar"><Input.Search allowClear placeholder="搜索标题" value={search} onChange={(event) => setSearch(event.target.value)} style={{ maxWidth: 340 }} /><Segmented value={level} onChange={(value) => setLevel(String(value))} options={[{ label: '全部', value: 'all' }, { label: 'Fatal', value: 'fatal' }, { label: 'Error', value: 'error' }, { label: 'Warning', value: 'warning' }]} /></Flex>
    <Table rowKey="id" columns={columns} dataSource={issues} onRow={(row) => ({ onClick: () => navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`) })} locale={{ emptyText: <Empty description="暂无错误事件" /> }} pagination={{ pageSize: 20, showSizeChanger: true }} />
  </Card></>
}

function IssueDetailPage() {
  const { projectId = '', issueId = '' } = useParams()
  const issuesQuery = useQuery({ queryKey: ['issues', projectId], queryFn: () => api.issues(projectId) })
  const eventsQuery = useQuery({ queryKey: ['events', projectId, issueId], queryFn: () => api.events(projectId, issueId) })
  if (issuesQuery.isLoading || eventsQuery.isLoading) return <Loading />
  if (issuesQuery.isError) return <ErrorView error={issuesQuery.error as Error} />
  if (eventsQuery.isError) return <ErrorView error={eventsQuery.error as Error} />
  const issue = issuesQuery.data?.find((item) => item.id === issueId)
  if (!issue) return <Result status="404" title="Issue 不存在" />
  const events = eventsQuery.data ?? []
  const latest = events[0]
  const frames = latest?.symbolicated_frames?.length ? latest.symbolicated_frames : latest?.frames ?? []
  return <><PageHeader title={issue.title} extra={<Space><Tag color={levelColor(issue.level)}>{issue.level || 'error'}</Tag><Statistic title="事件数" value={issue.count} /></Space>} />
    <div className="detail-grid"><Card title="Stack Trace" extra={latest && <Tag color={latest.symbolication_status === 'symbolicated' ? 'green' : 'orange'}>{latest.symbolication_status || 'not_attempted'}</Tag>}>
      {latest?.symbolication_status === 'miss' && <Alert type="warning" showIcon message="Source Map 未匹配" description="当前显示原始压缩栈；请检查 Release 与 Artifact 上传。" className="mb16" />}
      {frames.length ? <FrameList frames={frames} /> : <Empty description="该事件没有 Stack Trace" />}
    </Card><Card title="事件时间线"><Timeline items={events.slice(0, 20).map((event) => ({ color: levelColor(event.level), children: <Space direction="vertical"><Link to={`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(issueId)}?event=${encodeURIComponent(event.event_id)}`}>{event.title}</Link><Typography.Text type="secondary">{formatTime(event.received_at)} · {event.event_id}</Typography.Text></Space> }))} /></Card></div>
    {latest && <EventContext event={latest} />}
  </>
}

function FrameList({ frames }: { frames: Frame[] }) { return <div className="frame-list">{frames.map((frame, index) => <div className="frame" key={`${frame.filename}-${frame.lineno}-${index}`}><Flex justify="space-between"><Typography.Text code>{frame.function || '<anonymous>'}</Typography.Text><Tag>{frame.in_app ? 'in-app' : 'library'}</Tag></Flex><Typography.Text>{frame.filename || 'unknown'}:{frame.lineno ?? '?'}:{frame.colno ?? '?'}</Typography.Text>{frame.context_line && <Typography.Text type="secondary">{frame.context_line}</Typography.Text>}</div>)}</div> }
function EventContext({ event }: { event: Event }) { const tabs = [{ key: 'exception', label: 'Exception', children: <JsonView value={event.exception} /> }, { key: 'tags', label: 'Tags', children: <JsonView value={event.tags} /> }, { key: 'user', label: 'User', children: <JsonView value={event.user} /> }, { key: 'request', label: 'Request', children: <JsonView value={event.request} /> }, { key: 'breadcrumbs', label: 'Breadcrumbs', children: <Timeline items={(event.breadcrumbs ?? []).map((item) => ({ children: <Typography.Text>{String(item.message || item.category || '')}</Typography.Text> }))} /> }, { key: 'contexts', label: 'Contexts / Extra', children: <JsonView value={{ contexts: event.contexts, extra: event.extra }} /> }, { key: 'raw', label: 'Raw', children: <JsonView value={event.raw || event} /> }]; return <Card title={<Space><span>最新事件</span><Typography.Text type="secondary">{event.event_id}</Typography.Text></Space>} className="event-context"><Tabs items={tabs} /></Card> }

function ReleasesPage() {
  const { projectId = '' } = useParams(); const client = useQueryClient(); const [open, setOpen] = useState(false); const [form] = Form.useForm<{ version: string }>(); const query = useQuery({ queryKey: ['releases', projectId], queryFn: () => api.releases(projectId) }); const mutation = useMutation({ mutationFn: (version: string) => api.createRelease(projectId, version), onSuccess: () => { setOpen(false); form.resetFields(); void client.invalidateQueries({ queryKey: ['releases', projectId] }) } });
  if (query.isLoading) return <Loading />; if (query.isError) return <ErrorView error={query.error as Error} />; const columns: ColumnsType<Release> = [{ title: 'Version', dataIndex: 'version', render: (value: string) => <Typography.Text code>{value}</Typography.Text> }, { title: 'Created', dataIndex: 'created_at', render: formatTime }]; return <><PageHeader title="Releases" extra={<Button type="primary" onClick={() => setOpen(true)}>创建 Release</Button>} /><Card><Table rowKey={(row) => `${row.project_id}-${row.version}`} columns={columns} dataSource={query.data ?? []} locale={{ emptyText: <Empty description="暂无 Release" /> }} /></Card><Modal title="创建 Release" open={open} confirmLoading={mutation.isPending} onCancel={() => setOpen(false)} onOk={() => void form.validateFields().then((values) => mutation.mutate(values.version))}><Form form={form} layout="vertical"><Form.Item name="version" label="Version" rules={[{ required: true, message: '请输入版本' }]}><Input placeholder="web@1.0.0" /></Form.Item></Form></Modal></>
}

function SignalsPage() { const { projectId = '' } = useParams(); const query = useQuery({ queryKey: ['signals', projectId], queryFn: () => api.signals(projectId) }); if (query.isLoading) return <Loading />; if (query.isError) return <ErrorView error={query.error as Error} />; const columns: ColumnsType<Signal> = [{ title: 'Kind', dataIndex: 'kind', render: (value: string) => <Tag>{value}</Tag> }, { title: 'Event ID', dataIndex: 'event_id', render: (value?: string) => value || '—' }, { title: 'Received', dataIndex: 'received_at', render: formatTime }, { title: 'Size', dataIndex: 'size', render: (value?: number) => value ? `${value} B` : '—' }, { title: 'Payload', dataIndex: 'payload', render: (value: unknown) => <JsonView value={value} /> }]; return <><PageHeader title="Signals" /><Card><Table rowKey="id" columns={columns} dataSource={query.data ?? []} expandable={{ expandedRowRender: (row) => <JsonView value={row.payload} /> }} /></Card></> }

function ReportsPage() { const { projectId = '' } = useParams(); const query = useQuery({ queryKey: ['reports', projectId], queryFn: () => api.clientReports(projectId) }); if (query.isLoading) return <Loading />; if (query.isError) return <ErrorView error={query.error as Error} />; const reports = query.data ?? []; const discarded = reports.flatMap((report) => report.discarded_events ?? []).reduce((sum, item) => sum + (item.quantity ?? 0), 0); const columns: ColumnsType<ClientReport> = [{ title: 'Timestamp', dataIndex: 'timestamp', render: formatTime }, { title: 'Received', dataIndex: 'received_at', render: formatTime }, { title: 'Discarded', dataIndex: 'discarded_events', render: (items?: ClientReport['discarded_events']) => (items ?? []).map((item) => <Tag key={`${item.reason}-${item.category}`}>{item.reason || 'unknown'} / {item.category || 'unknown'}: {item.quantity ?? 0}</Tag>) }]; return <><PageHeader title="Client Reports" /><Flex gap={16} className="stats-row"><Card><Statistic title="Reports" value={reports.length} /></Card><Card><Statistic title="Discarded events" value={discarded} /></Card></Flex><Card><Table rowKey="id" columns={columns} dataSource={reports} /></Card></> }

function SettingsPage() { const { projectId = '' } = useParams(); const query = useQuery({ queryKey: ['project', 'default', projectId], queryFn: () => api.project('default', projectId) }); if (query.isLoading) return <Loading />; if (query.isError) return <ErrorView error={query.error as Error} />; const project = query.data as Project; return <><PageHeader title="Project Settings" /><Card title="Project"><Typography.Paragraph><Typography.Text strong>Name：</Typography.Text>{project.name}</Typography.Paragraph><Typography.Paragraph><Typography.Text strong>Slug：</Typography.Text>{project.slug}</Typography.Paragraph><Typography.Paragraph><Typography.Text strong>Platform：</Typography.Text>{project.platform || '—'}</Typography.Paragraph></Card><Card title="Project Keys" className="mt16"><Table rowKey="id" pagination={false} columns={[{ title: 'Name', dataIndex: 'name' }, { title: 'Public key', dataIndex: 'public', render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> }, { title: 'DSN', dataIndex: 'dsn', render: (value: string) => <Typography.Text code copyable ellipsis>{value}</Typography.Text> }]} dataSource={project.keys ?? []} locale={{ emptyText: <Empty description="暂无 Project Key" /> }} /></Card><Card title="Teams" className="mt16"><Space wrap>{(project.teams ?? []).map((team) => <Tag key={team.id}>{team.name}</Tag>)}</Space></Card></> }

function Root() { return <ConfigProvider theme={{ algorithm: theme.defaultAlgorithm, token: { colorPrimary: '#5b5bd6', borderRadius: 6 }, components: { Layout: { siderBg: '#171923', headerBg: '#fff' }, Table: { headerBg: '#fafafa' } } }}><AntApp><AppShell /></AntApp></ConfigProvider> }
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><QueryClientProvider client={queryClient}><BrowserRouter><Root /></BrowserRouter></QueryClientProvider></React.StrictMode>)
