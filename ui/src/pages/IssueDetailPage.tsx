import React, { useMemo, useState } from "react"
import {
  Button,
  Card,
  Empty,
  Flex,
  Result,
  Progress,
  Segmented,
  Space,
  Tag,
  Timeline,
  Typography,
} from "antd"
import {
  ReloadOutlined,
  HistoryOutlined,
} from "@ant-design/icons"
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom"
import { api, type Event } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "../components/Common"
import { StackTrace } from "../components/StackTrace"
import { EventContextView } from "../components/EventContextView"
import { SeriesChart } from "../components/SeriesChart"

const trendWindows = {
  "24h": { duration: 24 * 60 * 60 * 1000, resolution: "1h" },
  "7d": { duration: 7 * 24 * 60 * 60 * 1000, resolution: "1h" },
  "30d": { duration: 30 * 24 * 60 * 60 * 1000, resolution: "1d" },
} as const
const tagDimensions = ["browser.name", "os.name", "release", "url"]

export function IssueDetailPage() {
  const { projectId = "", issueId = "" } = useParams()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const selectedEventId = searchParams.get("event")
  const queryClient = useQueryClient()
  const [trendWindow, setTrendWindow] = useState<keyof typeof trendWindows>("24h")
  const range = useMemo(() => {
    const end = new Date()
    return { start: new Date(end.getTime() - trendWindows[trendWindow].duration).toISOString(), end: end.toISOString(), resolution: trendWindows[trendWindow].resolution }
  }, [trendWindow])

  const issueQuery = useQuery({
    queryKey: ["issue", projectId, issueId],
    queryFn: () => api.issue(projectId, issueId),
  })

  const eventsQuery = useQuery({
    queryKey: ["events", projectId, issueId],
    queryFn: () => api.events(projectId, issueId),
    refetchInterval: 15000,
  })

  const seriesQuery = useQuery({
    queryKey: ["issue-series", projectId, issueId, range],
    queryFn: () => api.issueSeries(projectId, issueId, range),
  })
  const tagQueries = useQueries({ queries: tagDimensions.map((key) => ({
    queryKey: ["issue-tags", projectId, issueId, key, range.start, range.end],
    queryFn: () => api.issueTagValues(projectId, issueId, key, { start: range.start, end: range.end, limit: 8 }),
  })) })

  const statusMutation = useMutation({
    mutationFn: (status: "resolved" | "unresolved" | "ignored") => api.updateIssue(issueId, status),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["issues", projectId] }),
  })

  if (issueQuery.isLoading || eventsQuery.isLoading) {
    return <Loading tip="正在加载 Issue 详情与事件列表..." />
  }

  if (issueQuery.isError) return <ErrorView error={issueQuery.error as Error} />
  if (eventsQuery.isError) return <ErrorView error={eventsQuery.error as Error} />

  const issue = issueQuery.data
  if (!issue) {
    return (
      <Result
        status="404"
        title="Issue 未找到"
        subTitle="指定的 Issue ID 不存在或已被清理。"
        extra={<Link to={`/projects/${encodeURIComponent(projectId)}/issues`}><Button type="primary">返回 Issue 列表</Button></Link>}
      />
    )
  }

  const events = eventsQuery.data ?? []
  const activeIndex = selectedEventId
    ? Math.max(0, events.findIndex((e) => e.event_id === selectedEventId))
    : 0
  const activeEvent: Event | undefined = events[activeIndex] ?? events[0]

  const goToEvent = (eventId: string) => {
    navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(issueId)}?event=${encodeURIComponent(eventId)}`)
  }

  return (
    <>
      <PageHeader
        title={issue.title}
        subtitle={activeEvent?.culprit ? `Culprit: ${activeEvent.culprit}` : `Group: ${issue.group_hash}`}
        breadcrumbItems={[{ title: <Link to={`/projects/${encodeURIComponent(projectId)}/issues`}>Issues</Link> }]}
        breadcrumbTitle={<Typography.Text strong style={{ color: "#6366f1" }}>#{issue.id.slice(0, 8)}</Typography.Text>}
        extra={
          <Space wrap size="small">
            <Tag color={levelColor(issue.level)} style={{ textTransform: "capitalize", fontSize: 12, margin: 0 }}>
              {issue.level || "error"}
            </Tag>
            <Tag style={{ margin: 0 }}>
              累计 {issue.count} 次
            </Tag>
            <Tag color={issue.status === "resolved" ? "green" : issue.status === "ignored" ? "gold" : "blue"} style={{ margin: 0 }}>
              {issue.status === "resolved" ? "已解决" : issue.status === "ignored" ? "已忽略" : issue.regression ? "回归" : "未解决"}
            </Tag>
            <Button size="small" onClick={() => statusMutation.mutate(issue.status === "resolved" ? "unresolved" : "resolved")}>
              {issue.status === "resolved" ? "重新打开" : "标记已解决"}
            </Button>
            <Button size="small" icon={<ReloadOutlined />} onClick={() => void eventsQuery.refetch()}>
              刷新
            </Button>
          </Space>
        }
      />

      <Card size="small" className="content-card mb12" title="错误趋势" extra={<Segmented size="small" value={trendWindow} onChange={(value) => setTrendWindow(value as keyof typeof trendWindows)} options={[{ label: "24h", value: "24h" }, { label: "7d", value: "7d" }, { label: "30d", value: "30d" }]} />}>
        <SeriesChart data={seriesQuery.data ?? []} />
      </Card>

      <Card size="small" className="content-card mb12" title="影响维度">
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(240px, 1fr))", gap: 16 }}>
          {tagDimensions.map((key, index) => {
            const values = tagQueries[index].data ?? []
            const total = values.reduce((sum, item) => sum + item.count, 0)
            return <div key={key}><Typography.Text strong>{key}</Typography.Text>{values.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无数据" /> : values.map((item) => <div key={item.value} style={{ marginTop: 8, cursor: "pointer" }} onClick={() => navigate(`/projects/${encodeURIComponent(projectId)}/issues?${key === "environment" || key === "release" ? `${encodeURIComponent(key)}=${encodeURIComponent(item.value)}` : `query=${encodeURIComponent(`${key}:${item.value}`)}`}`)}><Flex justify="space-between"><Typography.Text ellipsis={{ tooltip: item.value }} style={{ maxWidth: 180 }}>{item.value}</Typography.Text><Typography.Text type="secondary">{item.count}</Typography.Text></Flex><Progress percent={total ? Math.round(item.count * 100 / total) : 0} showInfo={false} size="small" /></div>)}</div>
          })}
        </div>
      </Card>

      {/* TOP SECTION: Event Details (Left) + Vertical Event Timeline (Right) - Equal Height */}
      <div className="issue-top-grid">
        {/* Left Column: Event Context Details Card */}
        <div>
          {activeEvent ? (
            <EventContextView event={activeEvent} />
          ) : (
            <Card className="content-card equal-height-card">
              <Empty description="该 Issue 下没有可展示的事件" />
            </Card>
          )}
        </div>

        {/* Right Column: Vertical Timeline Card */}
        <div>
          <Card
            size="small"
            title={
              <Space size="small">
                <HistoryOutlined style={{ color: "#6366f1" }} />
                <span>事件时间线</span>
              </Space>
            }
            extra={
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                共 {events.length} 个事件
              </Typography.Text>
            }
            className="content-card equal-height-card"
          >
            {events.length === 0 ? (
              <Empty description="暂无事件记录" />
            ) : (
              <Timeline
                style={{ marginTop: 8 }}
                items={events.map((ev) => {
                  const isSelected = ev.event_id === activeEvent?.event_id
                  return {
                    color: levelColor(ev.level),
                    children: (
                      <div
                        className={`timeline-event-box ${isSelected ? "is-active" : ""}`}
                        onClick={() => goToEvent(ev.event_id)}
                      >
                        <div className="timeline-event-title">
                          {ev.title || "Error Event"}
                        </div>
                        <div className="timeline-event-sub">
                          <span>{formatTime(ev.occurred_at || ev.received_at)}</span>
                          <span>·</span>
                          <Typography.Text code style={{ fontSize: 10 }}>#{ev.event_id.slice(0, 8)}</Typography.Text>
                          {ev.environment && (
                            <Tag style={{ margin: 0, fontSize: 9, padding: "0 3px", lineHeight: "14px" }}>
                              {ev.environment}
                            </Tag>
                          )}
                        </div>
                      </div>
                    ),
                  }
                })}
              />
            )}
          </Card>
        </div>
      </div>

      {/* BOTTOM SECTION (Full Width): Core Call Stack (StackTrace with Decompiled Code) */}
      {activeEvent && <StackTrace event={activeEvent} />}
    </>
  )
}
