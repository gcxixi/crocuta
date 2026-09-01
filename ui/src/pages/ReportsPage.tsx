import React from "react"
import { Button, Card, Empty, Flex, Space, Statistic, Table, Tag, Typography } from "antd"
import { ReloadOutlined, FileSearchOutlined, DisconnectOutlined } from "@ant-design/icons"
import { useQuery } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type ClientReport } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"

export function ReportsPage() {
  const { projectId = "" } = useParams()
  const query = useQuery({
    queryKey: ["reports", projectId],
    queryFn: () => api.clientReports(projectId),
    refetchInterval: 15000,
  })

  if (query.isLoading) return <Loading tip="正在加载 Client Reports..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const reports = query.data ?? []
  const totalDiscarded = reports
    .flatMap((report) => report.discarded_events ?? [])
    .reduce((sum, item) => sum + (item.quantity ?? 0), 0)

  const columns: ColumnsType<ClientReport> = [
    {
      title: "客户端时间 (Timestamp)",
      dataIndex: "timestamp",
      render: formatTime,
    },
    {
      title: "接收时间 (Received)",
      dataIndex: "received_at",
      render: formatTime,
    },
    {
      title: "丢弃事件原因统计 (Discarded Events)",
      dataIndex: "discarded_events",
      render: (items?: ClientReport["discarded_events"]) => (
        <Space wrap size={[6, 6]}>
          {(items ?? []).map((item, idx) => (
            <Tag key={`${item.reason}-${item.category}-${idx}`} color="volcano">
              <strong>{item.reason || "unknown"}</strong> / {item.category || "unknown"}:{" "}
              <span style={{ fontWeight: 700 }}>{item.quantity ?? 0}</span>
            </Tag>
          ))}
        </Space>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="Client Reports"
        subtitle="客户端 SDK 自动生成的采样与丢弃原因统计报告"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
            刷新
          </Button>
        }
      />

      <div className="stats-grid">
        <Card className="stat-card" size="small">
          <Statistic
            title={<Space><FileSearchOutlined style={{ color: "#6366f1" }} /><span>总上报数 (Reports)</span></Space>}
            value={reports.length}
            valueStyle={{ color: "#6366f1", fontWeight: 700 }}
          />
        </Card>
        <Card className="stat-card" size="small">
          <Statistic
            title={<Space><DisconnectOutlined style={{ color: "#ef4444" }} /><span>累计丢弃事件总数</span></Space>}
            value={totalDiscarded}
            valueStyle={{ color: "#ef4444", fontWeight: 700 }}
          />
        </Card>
      </div>

      <Card className="content-card">
        <Table
          rowKey="id"
          columns={columns}
          dataSource={reports}
          locale={{ emptyText: <Empty description="暂无客户端采样丢弃报告" /> }}
          pagination={{ pageSize: 20, showSizeChanger: true }}
        />
      </Card>
    </>
  )
}
