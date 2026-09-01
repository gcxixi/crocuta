import React, { useState } from "react"
import {
  Button,
  Card,
  Empty,
  Flex,
  Input,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
} from "antd"
import {
  ReloadOutlined,
  FundProjectionScreenOutlined,
  ThunderboltOutlined,
  VideoCameraOutlined,
} from "@ant-design/icons"
import { useQuery } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type Signal } from "../api"
import { ErrorView, JsonView, Loading, PageHeader, formatTime } from "../components/Common"

function signalKindColor(kind: string) {
  switch (kind) {
    case "transaction":
    case "span":
      return "purple"
    case "replay_event":
    case "replay_recording":
      return "geekblue"
    case "profile":
    case "profile_chunk":
      return "magenta"
    case "session":
    case "sessions":
      return "blue"
    case "log":
      return "orange"
    case "minidump":
    case "applecrashreport":
      return "red"
    default:
      return "default"
  }
}

export function SignalsPage() {
  const { projectId = "" } = useParams()
  const [kindFilter, setKindFilter] = useState("all")
  const [search, setSearch] = useState("")

  const query = useQuery({
    queryKey: ["signals", projectId],
    queryFn: () => api.signals(projectId),
    refetchInterval: 15000,
  })

  if (query.isLoading) return <Loading tip="正在加载 Signals..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const rawSignals = query.data ?? []
  const kinds = Array.from(new Set(rawSignals.map((s) => s.kind)))

  const filtered = rawSignals.filter((s) => {
    const matchKind = kindFilter === "all" || s.kind === kindFilter
    const matchSearch =
      !search ||
      s.id.toLowerCase().includes(search.toLowerCase()) ||
      (s.event_id && s.event_id.toLowerCase().includes(search.toLowerCase())) ||
      JSON.stringify(s.payload).toLowerCase().includes(search.toLowerCase())
    return matchKind && matchSearch
  })

  const txCount = rawSignals.filter((s) => s.kind.includes("transaction") || s.kind.includes("span")).length
  const replayCount = rawSignals.filter((s) => s.kind.includes("replay")).length

  const columns: ColumnsType<Signal> = [
    {
      title: "Kind / Type",
      dataIndex: "kind",
      width: 160,
      render: (value: string) => (
        <Tag color={signalKindColor(value)} style={{ fontWeight: 600 }}>
          {value}
        </Tag>
      ),
    },
    {
      title: "Signal ID",
      dataIndex: "id",
      width: 180,
      render: (v: string) => <Typography.Text code copyable>{v.slice(0, 12)}...</Typography.Text>,
    },
    {
      title: "Associated Event ID",
      dataIndex: "event_id",
      width: 180,
      render: (v?: string) =>
        v ? <Typography.Text code copyable>{v.slice(0, 12)}...</Typography.Text> : <Typography.Text type="secondary">—</Typography.Text>,
    },
    {
      title: "Payload Size",
      dataIndex: "size",
      width: 120,
      render: (v?: number) => (v ? `${(v / 1024).toFixed(1)} KB` : "—"),
    },
    {
      title: "Received",
      dataIndex: "received_at",
      render: formatTime,
    },
  ]

  return (
    <>
      <PageHeader
        title="Signals"
        subtitle="非错误类 Sentry Envelope 扩展信号 (Performance, Replay, Session, Profile 等)"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
            刷新
          </Button>
        }
      />

      <div className="stats-grid">
        <Card className="stat-card" size="small">
          <Statistic
            title={<Space><FundProjectionScreenOutlined style={{ color: "#6366f1" }} /><span>总 Signal 数</span></Space>}
            value={rawSignals.length}
            valueStyle={{ color: "#6366f1", fontWeight: 700 }}
          />
        </Card>
        <Card className="stat-card" size="small">
          <Statistic
            title={<Space><ThunderboltOutlined style={{ color: "#8b5cf6" }} /><span>Transactions / Spans</span></Space>}
            value={txCount}
            valueStyle={{ color: "#8b5cf6", fontWeight: 700 }}
          />
        </Card>
        <Card className="stat-card" size="small">
          <Statistic
            title={<Space><VideoCameraOutlined style={{ color: "#0ea5e9" }} /><span>Replay Recordings</span></Space>}
            value={replayCount}
            valueStyle={{ color: "#0ea5e9", fontWeight: 700 }}
          />
        </Card>
      </div>

      <Card className="content-card">
        <div className="filter-bar">
          <Flex gap={12} align="center" wrap="wrap">
            <Input.Search
              allowClear
              placeholder="搜索 Signal ID / 内容..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{ width: 280 }}
            />
            <Select
              value={kindFilter}
              onChange={setKindFilter}
              style={{ width: 180 }}
              options={[
                { label: "全部信号类型", value: "all" },
                ...kinds.map((k) => ({ label: k, value: k })),
              ]}
            />
          </Flex>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            共 {filtered.length} 条信号记录
          </Typography.Text>
        </div>

        <Table
          rowKey="id"
          columns={columns}
          dataSource={filtered}
          expandable={{
            expandedRowRender: (row) => (
              <div style={{ padding: "8px 0" }}>
                <Typography.Text strong style={{ marginBottom: 6, display: "block" }}>
                  Signal Payload:
                </Typography.Text>
                <JsonView value={row.payload} />
              </div>
            ),
          }}
          locale={{ emptyText: <Empty description="暂无 Signal 信号上报" /> }}
          pagination={{ pageSize: 20, showSizeChanger: true }}
        />
      </Card>
    </>
  )
}
