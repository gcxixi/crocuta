import React, { useMemo, useState } from "react"
import { BugOutlined, FundOutlined, TeamOutlined } from "@ant-design/icons"
import { Card, Col, Row, Segmented, Statistic } from "antd"
import { useQuery } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import { api } from "../api"
import { ErrorView, Loading, PageHeader } from "../components/Common"

const durations = { "24h": 24 * 60 * 60 * 1000, "7d": 7 * 24 * 60 * 60 * 1000, "30d": 30 * 24 * 60 * 60 * 1000 } as const

export function ProjectOverviewPage() {
  const { projectId = "" } = useParams()
  const [windowName, setWindowName] = useState<keyof typeof durations>("24h")
  const range = useMemo(() => {
    const end = new Date()
    return { start: new Date(end.getTime() - durations[windowName]).toISOString(), end: end.toISOString() }
  }, [windowName])
  const query = useQuery({ queryKey: ["project-stats", projectId, range], queryFn: () => api.projectStats("default", projectId, range), refetchInterval: 15000 })
  if (query.isLoading) return <Loading tip="正在加载项目概览..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />
  const stats = query.data ?? {}
  return <>
    <PageHeader title="项目概览" subtitle="统一时间窗内的错误、Issue 与影响用户" extra={<Segmented value={windowName} onChange={(value) => setWindowName(value as keyof typeof durations)} options={["24h", "7d", "30d"]} />} />
    <Row gutter={[16, 16]}>
      <Col xs={24} md={8}><Card className="content-card"><Statistic title="错误事件" value={stats.errors ?? 0} prefix={<BugOutlined style={{ color: "#ef4444" }} />} /></Card></Col>
      <Col xs={24} md={8}><Card className="content-card"><Statistic title="活跃 Issues" value={stats.issues ?? 0} prefix={<FundOutlined style={{ color: "#6366f1" }} />} /></Card></Col>
      <Col xs={24} md={8}><Card className="content-card"><Statistic title="影响用户" value={stats.users ?? 0} prefix={<TeamOutlined style={{ color: "#10b981" }} />} /></Card></Col>
    </Row>
  </>
}
