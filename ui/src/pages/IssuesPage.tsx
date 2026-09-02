import React, { useMemo, useState } from "react"
import { Badge, Button, Card, Empty, Flex, Input, Segmented, Select, Table, Tag, Typography } from "antd"
import { BugOutlined, CheckCircleOutlined, FireOutlined, ReloadOutlined, TeamOutlined, WarningOutlined } from "@ant-design/icons"
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type Issue } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"

const windows = {
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
} as const

export function IssuesPage() {
  const { projectId = "" } = useParams()
  const navigate = useNavigate()
  const [urlParams] = useSearchParams()
  const [search, setSearch] = useState(urlParams.get("query") ?? "")
  const [level, setLevel] = useState(urlParams.get("level") ?? "all")
  const [status, setStatus] = useState(urlParams.get("status") ?? "unresolved")
  const [sort, setSort] = useState(urlParams.get("sort") ?? "last_seen")
  const [windowName, setWindowName] = useState<keyof typeof windows | "all">("24h")
  const queryClient = useQueryClient()
  const start = windowName === "all" ? undefined : new Date(Date.now() - windows[windowName]).toISOString()

  const query = useInfiniteQuery({
    queryKey: ["issues", projectId, status, level, search, sort, windowName, urlParams.get("environment"), urlParams.get("release")],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.issuesPage(projectId, {
      status,
      level,
      query: search,
      sort,
      start,
      environment: urlParams.get("environment") ?? undefined,
      release: urlParams.get("release") ?? undefined,
      limit: 50,
      cursor: pageParam,
    }),
    getNextPageParam: (page) => page.nextCursor,
    refetchInterval: 15000,
  })

  const statusMutation = useMutation({
    mutationFn: ({ id, next }: { id: string; next: "resolved" | "unresolved" | "ignored" }) => api.updateIssue(id, next),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["issues", projectId] }),
  })
  const projectQuery = useQuery({ queryKey: ["project", "default", projectId], queryFn: () => api.project("default", projectId) })

  const issues = useMemo(() => query.data?.pages.flatMap((page) => page.data) ?? [], [query.data])
  if (query.isLoading) return <Loading tip="正在加载 Issues..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const totalEvents = issues.reduce((sum, item) => sum + item.count, 0)
  const totalUsers = issues.reduce((sum, item) => sum + (item.users || 0), 0)
  const fatalCount = issues.filter((item) => item.level === "fatal").length
  const columns: ColumnsType<Issue> = [
    {
      title: "Issue",
      dataIndex: "title",
      render: (value: string, row) => <div className="issue-title-cell"><Link to={`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`} onClick={(event) => event.stopPropagation()}><Typography.Text strong ellipsis={{ tooltip: value }}>{value}</Typography.Text></Link><Typography.Text type="secondary" className="issue-culprit" ellipsis>#{row.id.slice(0, 8)} · Group: {row.group_hash}</Typography.Text></div>,
    },
    { title: "Level", dataIndex: "level", width: 90, render: (value?: string) => <Tag color={levelColor(value)} style={{ textTransform: "capitalize", margin: 0 }}>{value || "error"}</Tag> },
    { title: "Events", dataIndex: "count", width: 90, render: (count: number) => <Badge count={count} overflowCount={99999} style={{ backgroundColor: "#6366f1" }} /> },
    { title: "Users", dataIndex: "users", width: 90, render: (users: number) => users ?? 0 },
    { title: "First Seen", dataIndex: "first_seen", width: 150, render: (value: string) => formatTime(value) },
    { title: "Last Seen", dataIndex: "last_seen", width: 150, render: (value: string) => formatTime(value) },
    {
      title: "状态", dataIndex: "status", width: 130,
      render: (value: Issue["status"], row) => <Select size="small" value={value ?? "unresolved"} onChange={(next) => statusMutation.mutate({ id: row.id, next })} onClick={(event) => event.stopPropagation()} style={{ width: 100 }} options={[{ label: "未解决", value: "unresolved" }, { label: "已解决", value: "resolved" }, { label: "忽略", value: "ignored" }]} />,
    },
  ]

  return <>
    <PageHeader title="Issues" subtitle="服务端筛选、稳定游标分页与错误聚合" extra={<Button size="small" icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新</Button>} />
    <div className="compact-stats-bar">
      <div className="compact-stat-item"><BugOutlined style={{ color: "#6366f1" }} /><span className="compact-stat-label">已加载 Issues:</span><span className="compact-stat-val">{issues.length}</span></div>
      <div className="compact-stat-item"><FireOutlined /><span className="compact-stat-label">Events:</span><span className="compact-stat-val">{totalEvents}</span></div>
      <div className="compact-stat-item"><TeamOutlined /><span className="compact-stat-label">Users:</span><span className="compact-stat-val">{totalUsers}</span></div>
      <div className="compact-stat-item"><WarningOutlined style={{ color: fatalCount ? "#ef4444" : "#10b981" }} /><span className="compact-stat-label">Fatal:</span><span className="compact-stat-val">{fatalCount}</span></div>
      <div className="compact-stat-item"><CheckCircleOutlined style={{ color: "#10b981" }} /><span className="compact-stat-label">状态:</span><span className="compact-stat-val">正常监控</span></div>
    </div>
    <Card className="content-card mb12" styles={{ body: { padding: 0 } }}>
      <div className="filter-bar">
        <Flex gap={8} align="center" wrap="wrap">
          <Input.Search allowClear size="small" placeholder="标题或 tag:value" value={search} onChange={(event) => setSearch(event.target.value)} style={{ width: 240 }} />
          <Select size="small" value={level} onChange={setLevel} style={{ width: 110 }} options={[{ label: "全部级别", value: "all" }, { label: "Fatal", value: "fatal" }, { label: "Error", value: "error" }, { label: "Warning", value: "warning" }, { label: "Info", value: "info" }]} />
          <Segmented size="small" value={status} onChange={(value) => setStatus(String(value))} options={[{ label: "未解决", value: "unresolved" }, { label: "全部", value: "all" }]} />
          <Select size="small" value={windowName} onChange={setWindowName} style={{ width: 100 }} options={[{ label: "24 小时", value: "24h" }, { label: "7 天", value: "7d" }, { label: "30 天", value: "30d" }, { label: "全部", value: "all" }]} />
          <Select size="small" value={sort} onChange={setSort} style={{ width: 130 }} options={[{ label: "最近发生", value: "last_seen" }, { label: "首次发生", value: "first_seen" }, { label: "事件数", value: "count" }, { label: "影响用户", value: "users" }]} />
        </Flex>
        <Typography.Text type="secondary">已加载 {issues.length} 条</Typography.Text>
      </div>
      <Table size="small" rowKey="id" columns={columns} dataSource={issues} pagination={false} onRow={(row) => ({ onClick: () => navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`), style: { cursor: "pointer" } })} locale={{ emptyText: <Empty description="暂无匹配的错误事件" /> }} />
      {query.hasNextPage && <Flex justify="center" style={{ padding: 16 }}><Button loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>加载更多</Button></Flex>}
    </Card>
    {issues.length === 0 && <SdkQuickStart dsn={projectQuery.data?.keys?.[0]?.dsn} />}
  </>
}
