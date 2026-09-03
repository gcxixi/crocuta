import React, { useMemo, useState } from "react"
import { App, Button, Card, Empty, Flex, Progress, Segmented, Space, Tag, Timeline, Typography } from "antd"
import { CopyOutlined, HistoryOutlined, ReloadOutlined } from "@ant-design/icons"
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate } from "react-router-dom"
import { api, type Event } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "./Common"
import { StackTrace } from "./StackTrace"
import { EventContextView } from "./EventContextView"
import { SeriesChart } from "./SeriesChart"

const trendWindows = {
  "24h": { duration: 24 * 60 * 60 * 1000, resolution: "1h" },
  "7d": { duration: 7 * 24 * 60 * 60 * 1000, resolution: "1h" },
  "30d": { duration: 30 * 24 * 60 * 60 * 1000, resolution: "1d" },
} as const
const tagDimensions = ["browser.name", "os.name", "release", "url"]

type IssueDetailViewProps = {
  projectId: string
  issueId: string
  embedded?: boolean
  eventId?: string | null
  onEventChange?: (eventId: string) => void
}

function snapshotMarkdown(issueTitle: string, issueId: string, event?: Event) {
  const frames = event?.symbolicated_frames?.length ? event.symbolicated_frames : event?.frames ?? []
  const stack = frames.slice(0, 12).map((frame) =>
    `- \`${frame.function || "<anonymous>"}\` — ${frame.filename || "unknown"}:${frame.lineno ?? "?"}:${frame.colno ?? "?"}`,
  ).join("\n") || "- 无可用堆栈"
  return [
    `# ${issueTitle}`,
    "",
    `- Issue: \`${issueId}\``,
    `- Event: \`${event?.event_id ?? "—"}\``,
    `- Level: ${event?.level ?? "error"}`,
    `- Release: ${event?.release ?? "—"}`,
    `- Environment: ${event?.environment ?? "—"}`,
    `- Time: ${formatTime(event?.occurred_at || event?.received_at)}`,
    "",
    "## Stack",
    stack,
  ].join("\n")
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    return
  } catch {
    const textarea = document.createElement("textarea")
    textarea.value = text
    textarea.setAttribute("readonly", "")
    textarea.style.position = "fixed"
    textarea.style.opacity = "0"
    document.body.appendChild(textarea)
    textarea.select()
    const copied = document.execCommand("copy")
    textarea.remove()
    if (!copied) throw new Error("copy unavailable")
  }
}

export function IssueDetailView({ projectId, issueId, embedded = false, eventId, onEventChange }: IssueDetailViewProps) {
  const { message } = App.useApp()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [trendWindow, setTrendWindow] = useState<keyof typeof trendWindows>("24h")
  const [localEventId, setLocalEventId] = useState<string | null>(eventId ?? null)
  const range = useMemo(() => {
    const end = new Date()
    return { start: new Date(end.getTime() - trendWindows[trendWindow].duration).toISOString(), end: end.toISOString(), resolution: trendWindows[trendWindow].resolution }
  }, [trendWindow])

  const issueQuery = useQuery({ queryKey: ["issue", projectId, issueId], queryFn: () => api.issue(projectId, issueId), enabled: Boolean(issueId) })
  const eventsQuery = useQuery({ queryKey: ["events", projectId, issueId], queryFn: () => api.events(projectId, issueId), enabled: Boolean(issueId), refetchInterval: 15000 })
  const seriesQuery = useQuery({ queryKey: ["issue-series", projectId, issueId, range], queryFn: () => api.issueSeries(projectId, issueId, range), enabled: Boolean(issueId) })
  const tagQueries = useQueries({ queries: tagDimensions.map((key) => ({
    queryKey: ["issue-tags", projectId, issueId, key, range.start, range.end],
    queryFn: () => api.issueTagValues(projectId, issueId, key, { start: range.start, end: range.end, limit: 8 }),
    enabled: Boolean(issueId),
  })) })
  const statusMutation = useMutation({
    mutationFn: (status: "resolved" | "unresolved" | "ignored") => api.updateIssue(issueId, status),
    onSuccess: () => {
      message.success("Issue 状态已更新")
      void queryClient.invalidateQueries({ queryKey: ["issue", projectId, issueId] })
      void queryClient.invalidateQueries({ queryKey: ["issues", projectId] })
    },
    onError: (error: Error) => message.error(`状态更新失败：${error.message}`),
  })

  if (issueQuery.isLoading || eventsQuery.isLoading) return <Loading tip="正在加载 Issue 详情..." />
  if (issueQuery.isError) return <ErrorView error={issueQuery.error as Error} retry={() => void issueQuery.refetch()} />
  if (eventsQuery.isError) return <ErrorView error={eventsQuery.error as Error} retry={() => void eventsQuery.refetch()} />
  const issue = issueQuery.data
  if (!issue) return <Empty description="Issue 不存在或已被清理" />
  const events = eventsQuery.data ?? []
  const selectedEventId = eventId ?? localEventId
  const activeEvent = events.find((item) => item.event_id === selectedEventId) ?? events[0]
  const selectEvent = (nextEventId: string) => {
    setLocalEventId(nextEventId)
    onEventChange?.(nextEventId)
  }
  const copySnapshot = async () => {
    try {
      await copyText(snapshotMarkdown(issue.title, issue.id, activeEvent))
      message.success("诊断快照已复制")
    } catch {
      message.error("无法访问剪贴板，请检查浏览器权限")
    }
  }
  const headerActions = <Space wrap size={6}>
    <Tag color={levelColor(issue.level)}>{issue.level || "error"}</Tag>
    <Tag>{issue.count} 次</Tag>
    <Tag color={issue.status === "resolved" ? "green" : issue.status === "ignored" ? "gold" : "blue"}>{issue.status === "resolved" ? "已解决" : issue.status === "ignored" ? "已忽略" : issue.regression ? "回归" : "未解决"}</Tag>
    <Button icon={<CopyOutlined />} onClick={() => void copySnapshot()}>复制快照</Button>
    <Button loading={statusMutation.isPending} onClick={() => statusMutation.mutate(issue.status === "resolved" ? "unresolved" : "resolved")}>{issue.status === "resolved" ? "重新打开" : "标记已解决"}</Button>
    <Button icon={<ReloadOutlined />} onClick={() => void eventsQuery.refetch()} aria-label="刷新事件" />
  </Space>

  return <section className={embedded ? "issue-detail-view is-embedded" : "issue-detail-view"}>
    {embedded ? <Flex className="embedded-detail-header" justify="space-between" align="center" gap={8}><div className="embedded-detail-title"><Typography.Text strong ellipsis={{ tooltip: issue.title }}>{issue.title}</Typography.Text><Typography.Text type="secondary">#{issue.id.slice(0, 8)}</Typography.Text></div>{headerActions}</Flex> : <PageHeader title={issue.title} subtitle={activeEvent?.culprit ? `触发位置：${activeEvent.culprit}` : `分组：${issue.group_hash}`} breadcrumbItems={[{ title: <Link to={`/projects/${encodeURIComponent(projectId)}/issues`}>Issues</Link> }]} breadcrumbTitle={<Typography.Text strong>#{issue.id.slice(0, 8)}</Typography.Text>} extra={headerActions} />}

    <div className="issue-detail-workbench">
      <main className="issue-detail-primary">
        {activeEvent ? <StackTrace event={activeEvent} /> : <Card className="content-card"><Empty description="该 Issue 暂无事件与堆栈" /></Card>}
        {activeEvent && <EventContextView event={activeEvent} />}
        <Card size="small" className="content-card" title="影响维度">
          <div className="impact-grid">
            {tagDimensions.map((key, index) => {
              const values = tagQueries[index].data ?? []
              const total = values.reduce((sum, item) => sum + item.count, 0)
              return <section key={key} className="impact-dimension"><Typography.Text strong>{key}</Typography.Text>{values.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" /> : values.map((item) => <button type="button" key={item.value} className="impact-value" onClick={() => navigate(`/projects/${encodeURIComponent(projectId)}/issues?${key === "release" ? `release=${encodeURIComponent(item.value)}` : `query=${encodeURIComponent(`${key}:${item.value}`)}`}`)}><Flex justify="space-between"><Typography.Text ellipsis={{ tooltip: item.value }}>{item.value}</Typography.Text><Typography.Text type="secondary">{item.count}</Typography.Text></Flex><Progress percent={total ? Math.round(item.count * 100 / total) : 0} showInfo={false} size="small" /></button>)}</section>
            })}
          </div>
        </Card>
      </main>

      <aside className="issue-detail-aside" aria-label="Issue 趋势和事件">
        <Card size="small" className="content-card" title="错误趋势" extra={<Segmented value={trendWindow} onChange={(value) => setTrendWindow(value as keyof typeof trendWindows)} options={["24h", "7d", "30d"]} />}>
          <SeriesChart data={seriesQuery.data ?? []} compact />
        </Card>
        <Card size="small" className="content-card timeline-card" title={<Space><HistoryOutlined /><span>事件时间线</span></Space>} extra={<Typography.Text type="secondary">{events.length} 个</Typography.Text>}>
          {events.length === 0 ? <Empty description="暂无事件" /> : <Timeline items={events.map((item) => ({ color: levelColor(item.level), content: <button type="button" className={`timeline-event-box ${item.event_id === activeEvent?.event_id ? "is-active" : ""}`} aria-current={item.event_id === activeEvent?.event_id ? "true" : undefined} onClick={() => selectEvent(item.event_id)}><span className="timeline-event-title">{item.title || "错误事件"}</span><span className="timeline-event-sub"><span>{formatTime(item.occurred_at || item.received_at)}</span><code>#{item.event_id.slice(0, 8)}</code></span></button> }))} />}
        </Card>
      </aside>
    </div>
  </section>
}
