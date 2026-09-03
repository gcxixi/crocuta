import React, { useEffect, useMemo, useState } from "react"
import { App, Badge, Button, Card, Empty, Flex, Input, Segmented, Select, Space, Table, Tag, Tooltip, Typography } from "antd"
import { AppstoreOutlined, BugOutlined, CheckOutlined, FireOutlined, ReloadOutlined, StopOutlined, TableOutlined, TeamOutlined, WarningOutlined } from "@ant-design/icons"
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type Issue } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"
import { IssueDetailView } from "../components/IssueDetailView"

const windows = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
} as const

function isEditableTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName) || Boolean(target.closest('[role="dialog"], [data-shortcuts-disabled="true"]'))
}

export function IssuesPage() {
  const { message } = App.useApp()
  const { projectId = "" } = useParams()
  const navigate = useNavigate()
  const [urlParams] = useSearchParams()
  const [search, setSearch] = useState(urlParams.get("query") ?? "")
  const [level, setLevel] = useState(urlParams.get("level") ?? "all")
  const [status, setStatus] = useState(urlParams.get("status") ?? "unresolved")
  const [sort, setSort] = useState(urlParams.get("sort") ?? "last_seen")
  const [windowName, setWindowName] = useState<keyof typeof windows | "all">("24h")
  const [viewMode, setViewMode] = useState<"table" | "split">("table")
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([])
  const [activeIssueId, setActiveIssueId] = useState<string | null>(null)
  const queryClient = useQueryClient()
  const start = windowName === "all" ? undefined : new Date(Date.now() - windows[windowName]).toISOString()

  const query = useInfiniteQuery({
    queryKey: ["issues", projectId, status, level, search, sort, windowName, urlParams.get("environment"), urlParams.get("release")],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.issuesPage(projectId, {
      status, level, query: search, sort, start,
      environment: urlParams.get("environment") ?? undefined,
      release: urlParams.get("release") ?? undefined,
      limit: 50, cursor: pageParam,
    }),
    getNextPageParam: (page) => page.nextCursor,
    refetchInterval: 15000,
  })
  const issues = useMemo(() => query.data?.pages.flatMap((page) => page.data) ?? [], [query.data])
  const activeIssue = issues.find((item) => item.id === activeIssueId) ?? issues[0]

  const refreshIssues = () => void queryClient.invalidateQueries({ queryKey: ["issues", projectId] })
  const statusMutation = useMutation({
    mutationFn: ({ id, next }: { id: string; next: "resolved" | "unresolved" | "ignored" }) => api.updateIssue(id, next),
    onSuccess: (_result, variables) => {
      message.success(variables.next === "resolved" ? "Issue 已解决" : variables.next === "ignored" ? "Issue 已忽略" : "Issue 已重新打开")
      refreshIssues()
    },
    onError: (error: Error) => message.error(`状态更新失败：${error.message}`),
  })
  const batchMutation = useMutation({
    mutationFn: async ({ ids, next }: { ids: string[]; next: "resolved" | "ignored" }) => {
      const results = await Promise.allSettled(ids.map((id) => api.updateIssue(id, next)))
      return { next, total: ids.length, failed: results.filter((item) => item.status === "rejected").length }
    },
    onSuccess: ({ next, total, failed }) => {
      if (failed) message.warning(`${total - failed} 条已${next === "resolved" ? "解决" : "忽略"}，${failed} 条失败`)
      else message.success(`${total} 条 Issue 已${next === "resolved" ? "解决" : "忽略"}`)
      setSelectedRowKeys([])
      refreshIssues()
    },
  })
  const projectQuery = useQuery({ queryKey: ["project", "default", projectId], queryFn: () => api.project("default", projectId) })

  useEffect(() => {
    if (viewMode === "split" && (!activeIssueId || !issues.some((item) => item.id === activeIssueId))) setActiveIssueId(issues[0]?.id ?? null)
  }, [activeIssueId, issues, viewMode])

  useEffect(() => {
    if (viewMode !== "split") return
    const handleKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.repeat || event.metaKey || event.ctrlKey || event.altKey || isEditableTarget(event.target)) return
      const key = event.key.toLowerCase()
      const index = issues.findIndex((item) => item.id === activeIssue?.id)
      if (key === "j" && index < issues.length - 1) {
        event.preventDefault()
        setActiveIssueId(issues[index + 1].id)
      } else if (key === "k" && index > 0) {
        event.preventDefault()
        setActiveIssueId(issues[index - 1].id)
      } else if (key === "e" && activeIssue && activeIssue.status !== "resolved") {
        event.preventDefault()
        statusMutation.mutate({ id: activeIssue.id, next: "resolved" })
      }
    }
    window.addEventListener("keydown", handleKey)
    return () => window.removeEventListener("keydown", handleKey)
  }, [activeIssue, issues, statusMutation, viewMode])

  if (query.isLoading) return <Loading tip="正在加载 Issues..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const totalEvents = issues.reduce((sum, item) => sum + item.count, 0)
  const totalUsers = issues.reduce((sum, item) => sum + (item.users || 0), 0)
  const fatalCount = issues.filter((item) => item.level === "fatal").length
  const rowAction = (row: Issue) => <Space size={2} className="row-quick-actions" data-shortcuts-disabled="true">
    <Tooltip title="解决"><Button type="text" icon={<CheckOutlined />} aria-label={`解决 ${row.title}`} onClick={(event) => { event.stopPropagation(); statusMutation.mutate({ id: row.id, next: "resolved" }) }} /></Tooltip>
    <Tooltip title="忽略"><Button type="text" icon={<StopOutlined />} aria-label={`忽略 ${row.title}`} onClick={(event) => { event.stopPropagation(); statusMutation.mutate({ id: row.id, next: "ignored" }) }} /></Tooltip>
  </Space>
  const columns: ColumnsType<Issue> = [
    { title: "Issue", dataIndex: "title", render: (value: string, row) => <div className="issue-title-cell"><Link to={`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`} onClick={(event) => event.stopPropagation()}><Typography.Text strong ellipsis={{ tooltip: value }}>{value}</Typography.Text></Link><Typography.Text type="secondary" className="issue-culprit" ellipsis>#{row.id.slice(0, 8)} · {row.group_hash}</Typography.Text></div> },
    { title: "级别", dataIndex: "level", width: 76, render: (value?: string) => <Tag color={levelColor(value)}>{value || "error"}</Tag> },
    { title: "事件", dataIndex: "count", width: 72, align: "right", render: (count: number) => <span className="numeric-cell">{count.toLocaleString()}</span> },
    { title: "用户", dataIndex: "users", width: 72, align: "right", render: (users: number) => <span className="numeric-cell">{(users ?? 0).toLocaleString()}</span> },
    { title: "首次发生", dataIndex: "first_seen", width: 146, render: (value: string) => formatTime(value) },
    { title: "最近发生", dataIndex: "last_seen", width: 146, render: (value: string) => formatTime(value) },
    { title: "状态", dataIndex: "status", width: 92, render: (value: Issue["status"]) => <Tag color={value === "resolved" ? "green" : value === "ignored" ? "gold" : "blue"}>{value === "resolved" ? "已解决" : value === "ignored" ? "已忽略" : "未解决"}</Tag> },
    { title: "快捷操作", key: "actions", width: 82, render: (_value, row) => rowAction(row) },
  ]

  const filters = <Flex gap={6} align="center" wrap="wrap">
    <Input.Search allowClear placeholder="标题或 tag:value" value={search} onChange={(event) => setSearch(event.target.value)} className="issue-search" />
    <Select value={level} onChange={setLevel} className="filter-select level-filter" options={[{ label: "全部级别", value: "all" }, { label: "Fatal", value: "fatal" }, { label: "Error", value: "error" }, { label: "Warning", value: "warning" }, { label: "Info", value: "info" }]} />
    <Segmented value={status} onChange={(value) => setStatus(String(value))} options={[{ label: "未解决", value: "unresolved" }, { label: "全部", value: "all" }]} />
    <Select value={windowName} onChange={setWindowName} className="filter-select time-filter" options={[{ label: "24 小时", value: "24h" }, { label: "7 天", value: "7d" }, { label: "30 天", value: "30d" }, { label: "全部", value: "all" }]} />
    <Select value={sort} onChange={setSort} className="filter-select sort-filter" options={[{ label: "最近发生", value: "last_seen" }, { label: "首次发生", value: "first_seen" }, { label: "事件数", value: "count" }, { label: "影响用户", value: "users" }]} />
  </Flex>

  return <section className={viewMode === "split" ? "issues-page is-split" : "issues-page"}>
    <PageHeader title="Issues" subtitle="错误聚合与排查工作台" extra={<Space><Segmented value={viewMode} onChange={(value) => setViewMode(value as "table" | "split")} options={[{ label: <span><TableOutlined /> 表格</span>, value: "table" }, { label: <span><AppstoreOutlined /> 分屏</span>, value: "split" }]} /><Button icon={<ReloadOutlined />} onClick={() => void query.refetch()} aria-label="刷新 Issues" /></Space>} />
    <div className="compact-stats-bar" aria-label="当前已加载指标">
      <div className="compact-stat-item"><BugOutlined /><span className="compact-stat-label">Issues</span><span className="compact-stat-val">{issues.length}</span></div>
      <div className="compact-stat-item"><FireOutlined /><span className="compact-stat-label">Events</span><span className="compact-stat-val">{totalEvents.toLocaleString()}</span></div>
      <div className="compact-stat-item"><TeamOutlined /><span className="compact-stat-label">Users</span><span className="compact-stat-val">{totalUsers.toLocaleString()}</span></div>
      <div className="compact-stat-item"><WarningOutlined className={fatalCount ? "severity-fatal" : "status-ok"} /><span className="compact-stat-label">Fatal</span><span className="compact-stat-val">{fatalCount}</span></div>
      <div className="compact-stat-item"><Badge status="processing" text="在线" /></div>
    </div>

    {selectedRowKeys.length > 0 && <div className="bulk-action-bar" role="region" aria-label="批量操作"><Typography.Text strong>已选择 {selectedRowKeys.length} 条</Typography.Text><Space><Button icon={<CheckOutlined />} loading={batchMutation.isPending} onClick={() => batchMutation.mutate({ ids: selectedRowKeys.map(String), next: "resolved" })}>批量解决</Button><Button icon={<StopOutlined />} loading={batchMutation.isPending} onClick={() => batchMutation.mutate({ ids: selectedRowKeys.map(String), next: "ignored" })}>批量忽略</Button><Button type="text" onClick={() => setSelectedRowKeys([])}>取消选择</Button></Space></div>}

    {viewMode === "table" ? <Card className="content-card issue-table-card" styles={{ body: { padding: 0 } }}>
      <div className="filter-bar">{filters}<Typography.Text type="secondary">已加载 {issues.length} 条</Typography.Text></div>
      <Table size="small" rowKey="id" columns={columns} dataSource={issues} pagination={false} scroll={{ x: 1040 }} rowSelection={{ selectedRowKeys, preserveSelectedRowKeys: true, onChange: setSelectedRowKeys }} onRow={(row) => ({ onClick: () => navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`), style: { cursor: "pointer" } })} locale={{ emptyText: <Empty description="暂无匹配的错误事件" /> }} />
      {query.hasNextPage && <Flex justify="center" className="load-more"><Button loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>加载更多</Button></Flex>}
    </Card> : <div className="issues-workbench">
      <section className="issues-master-pane" aria-label="Issue 列表">
        <div className="filter-bar split-filter">{filters}<Typography.Text type="secondary">J/K 切换 · E 解决</Typography.Text></div>
        <div className="issue-master-list">
          {issues.map((issue) => <div key={issue.id} className={`issue-master-row ${issue.id === activeIssue?.id ? "is-active" : ""}`}><button type="button" className="issue-master-select" aria-current={issue.id === activeIssue?.id ? "true" : undefined} onClick={() => setActiveIssueId(issue.id)}><span className="issue-master-title">{issue.title}</span><span className="issue-master-meta"><Tag color={levelColor(issue.level)}>{issue.level || "error"}</Tag><span>{issue.count} 事件</span><span>{formatTime(issue.last_seen)}</span></span></button>{rowAction(issue)}</div>)}
          {issues.length === 0 && <Empty description="暂无匹配的错误事件" />}
        </div>
        {query.hasNextPage && <Flex justify="center" className="load-more"><Button loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>加载更多</Button></Flex>}
      </section>
      <section className="issues-detail-pane" aria-label="Issue 详情">{activeIssue ? <IssueDetailView key={activeIssue.id} projectId={projectId} issueId={activeIssue.id} embedded /> : <Empty description="选择一条 Issue 开始排查" />}</section>
    </div>}
    {issues.length === 0 && <SdkQuickStart dsn={projectQuery.data?.keys?.[0]?.dsn} />}
  </section>
}
