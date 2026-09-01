import React from "react"
import {
  Button,
  Card,
  Empty,
  Result,
  Space,
  Tag,
  Timeline,
  Typography,
} from "antd"
import {
  ReloadOutlined,
  HistoryOutlined,
} from "@ant-design/icons"
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom"
import { api, type Event } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "../components/Common"
import { StackTrace } from "../components/StackTrace"
import { EventContextView } from "../components/EventContextView"

export function IssueDetailPage() {
  const { projectId = "", issueId = "" } = useParams()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const selectedEventId = searchParams.get("event")

  const issuesQuery = useQuery({
    queryKey: ["issues", projectId],
    queryFn: () => api.issues(projectId),
  })

  const eventsQuery = useQuery({
    queryKey: ["events", projectId, issueId],
    queryFn: () => api.events(projectId, issueId),
    refetchInterval: 15000,
  })

  if (issuesQuery.isLoading || eventsQuery.isLoading) {
    return <Loading tip="正在加载 Issue 详情与事件列表..." />
  }

  if (issuesQuery.isError) return <ErrorView error={issuesQuery.error as Error} />
  if (eventsQuery.isError) return <ErrorView error={eventsQuery.error as Error} />

  const issue = issuesQuery.data?.find((item) => item.id === issueId)
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
            <Button size="small" icon={<ReloadOutlined />} onClick={() => void eventsQuery.refetch()}>
              刷新
            </Button>
          </Space>
        }
      />

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
