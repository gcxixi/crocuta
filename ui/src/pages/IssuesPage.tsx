import React, { useState } from "react"
import {
  Badge,
  Button,
  Card,
  Empty,
  Flex,
  Input,
  Select,
  Segmented,
  Space,
  Table,
  Tag,
  Typography,
} from "antd"
import {
  ReloadOutlined,
  BugOutlined,
  FireOutlined,
  WarningOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons"
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate, useParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type Issue } from "../api"
import { ErrorView, Loading, PageHeader, formatTime, levelColor } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"

export function IssuesPage() {
  const { projectId = "" } = useParams()
  const navigate = useNavigate()
  const [search, setSearch] = useState("")
  const [level, setLevel] = useState("all")
  const [status, setStatus] = useState("unresolved")

  const query = useQuery({
    queryKey: ["issues", projectId],
    queryFn: () => api.issues(projectId),
    refetchInterval: 15000,
  })

  const projectQuery = useQuery({
    queryKey: ["project", "default", projectId],
    queryFn: () => api.project("default", projectId),
  })

  if (query.isLoading) return <Loading tip="正在加载 Issues..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const rawIssues = query.data ?? []
  const totalEvents = rawIssues.reduce((sum, item) => sum + (item.count || 0), 0)
  const fatalCount = rawIssues.filter((i) => i.level === "fatal").length

  const filteredIssues = rawIssues.filter((item) => {
    const matchSearch = !search || item.title.toLowerCase().includes(search.toLowerCase())
    const matchLevel = level === "all" || item.level === level
    return matchSearch && matchLevel
  })

  const columns: ColumnsType<Issue> = [
    {
      title: "Issue",
      dataIndex: "title",
      render: (value: string, row) => (
        <div className="issue-title-cell">
          <Link
            to={`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`}
            onClick={(e) => e.stopPropagation()}
          >
            <Typography.Text strong style={{ fontSize: 13 }} ellipsis={{ tooltip: value }}>
              {value}
            </Typography.Text>
          </Link>
          <Typography.Text type="secondary" className="issue-culprit" ellipsis>
            #{row.id.slice(0, 8)} · Group: {row.group_hash}
          </Typography.Text>
        </div>
      ),
    },
    {
      title: "Level",
      dataIndex: "level",
      width: 90,
      render: (value?: string) => (
        <Tag color={levelColor(value)} style={{ textTransform: "capitalize", margin: 0, fontSize: 11 }}>
          {value || "error"}
        </Tag>
      ),
    },
    {
      title: "Events",
      dataIndex: "count",
      width: 90,
      sorter: (a, b) => a.count - b.count,
      render: (count: number) => (
        <Badge count={count} overflowCount={99999} style={{ backgroundColor: "#6366f1", fontSize: 11 }} />
      ),
    },
    {
      title: "First Seen",
      dataIndex: "first_seen",
      width: 150,
      render: (v: string) => <span style={{ fontSize: 12 }}>{formatTime(v)}</span>,
    },
    {
      title: "Last Seen",
      dataIndex: "last_seen",
      width: 150,
      render: (v: string) => <span style={{ fontSize: 12 }}>{formatTime(v)}</span>,
      sorter: (a, b) => new Date(a.last_seen).getTime() - new Date(b.last_seen).getTime(),
      defaultSortOrder: "descend",
    },
  ]

  const defaultDsn = projectQuery.data?.keys?.[0]?.dsn

  return (
    <>
      <PageHeader
        title="Issues"
        subtitle="错误事件聚合与分组监控"
        extra={
          <Button size="small" icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
            刷新
          </Button>
        }
      />

      {/* High-Density Compact Stats Bar */}
      <div className="compact-stats-bar">
        <div className="compact-stat-item">
          <BugOutlined style={{ color: "#6366f1", fontSize: 13 }} />
          <span className="compact-stat-label">Issues:</span>
          <span className="compact-stat-val" style={{ color: "#6366f1" }}>{rawIssues.length}</span>
        </div>
        <div className="compact-stat-item">
          <FireOutlined style={{ color: "#0f172a", fontSize: 13 }} />
          <span className="compact-stat-label">总事件数:</span>
          <span className="compact-stat-val">{totalEvents}</span>
        </div>
        <div className="compact-stat-item">
          <WarningOutlined style={{ color: fatalCount > 0 ? "#ef4444" : "#10b981", fontSize: 13 }} />
          <span className="compact-stat-label">Fatal:</span>
          <span className="compact-stat-val" style={{ color: fatalCount > 0 ? "#ef4444" : "#10b981" }}>{fatalCount}</span>
        </div>
        <div className="compact-stat-item">
          <CheckCircleOutlined style={{ color: "#10b981", fontSize: 13 }} />
          <span className="compact-stat-label">状态:</span>
          <span className="compact-stat-val" style={{ color: "#10b981", fontSize: 13 }}>正常监控</span>
        </div>
      </div>

      {/* Main Table Card */}
      <Card className="content-card mb12" styles={{ body: { padding: 0 } }}>
        <div className="filter-bar">
          <Flex gap={8} align="center" wrap="wrap">
            <Input.Search
              allowClear
              size="small"
              placeholder="搜索 Issue..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{ width: 220 }}
            />
            <Select
              size="small"
              value={level}
              onChange={setLevel}
              style={{ width: 110 }}
              options={[
                { label: "全部级别", value: "all" },
                { label: "Fatal", value: "fatal" },
                { label: "Error", value: "error" },
                { label: "Warning", value: "warning" },
                { label: "Info", value: "info" },
              ]}
            />
            <Segmented
              size="small"
              value={status}
              onChange={(v) => setStatus(String(v))}
              options={[
                { label: "未解决", value: "unresolved" },
                { label: "全部", value: "all" },
              ]}
            />
          </Flex>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            共 {filteredIssues.length} 个 Issue
          </Typography.Text>
        </div>

        <Table
          size="small"
          rowKey="id"
          columns={columns}
          dataSource={filteredIssues}
          onRow={(row) => ({
            onClick: () => navigate(`/projects/${encodeURIComponent(projectId)}/issues/${encodeURIComponent(row.id)}`),
            style: { cursor: "pointer" },
          })}
          locale={{
            emptyText: (
              <div style={{ padding: "24px 0" }}>
                <Empty description="暂无匹配的错误事件" />
              </div>
            ),
          }}
          pagination={{ pageSize: 25, size: "small", showSizeChanger: true, showTotal: (t) => `共 ${t} 条` }}
        />
      </Card>

      {rawIssues.length === 0 && <SdkQuickStart dsn={defaultDsn} />}
    </>
  )
}
