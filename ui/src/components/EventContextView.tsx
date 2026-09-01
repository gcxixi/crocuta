import React from "react"
import { Card, Descriptions, Empty, Space, Table, Tabs, Tag, Typography } from "antd"
import {
  BugOutlined,
  TagsOutlined,
  UserOutlined,
  GlobalOutlined,
  HistoryOutlined,
  CodeOutlined,
  SlidersOutlined,
} from "@ant-design/icons"
import type { Event } from "../api"
import { JsonView, formatTime } from "./Common"
import { BreadcrumbsTimeline } from "./BreadcrumbsTimeline"

export function EventContextView({ event }: { event: Event }) {
  const userText = event.user
    ? event.user.email || event.user.username || event.user.id || event.user.ip_address
    : null

  const tabs = [
    {
      key: "exception",
      label: (
        <Space size={4}>
          <BugOutlined />
          <span>Exception</span>
        </Space>
      ),
      children: <ExceptionTab event={event} />,
    },
    {
      key: "breadcrumbs",
      label: (
        <Space size={4}>
          <HistoryOutlined />
          <span>Breadcrumbs ({event.breadcrumbs?.length ?? 0})</span>
        </Space>
      ),
      children: <BreadcrumbsTimeline breadcrumbs={event.breadcrumbs} />,
    },
    {
      key: "request",
      label: (
        <Space size={4}>
          <GlobalOutlined />
          <span>Request</span>
        </Space>
      ),
      children: <RequestTab event={event} />,
    },
    {
      key: "user",
      label: (
        <Space size={4}>
          <UserOutlined />
          <span>User & Client</span>
        </Space>
      ),
      children: <UserTab event={event} />,
    },
    {
      key: "tags",
      label: (
        <Space size={4}>
          <TagsOutlined />
          <span>Tags & Contexts</span>
        </Space>
      ),
      children: <TagsAndContextsTab event={event} />,
    },
    {
      key: "raw",
      label: (
        <Space size={4}>
          <CodeOutlined />
          <span>Raw JSON</span>
        </Space>
      ),
      children: <JsonView value={event.raw || event} maxHeight={340} />,
    },
  ]

  return (
    <Card
      size="small"
      title={
        <Space size="middle" wrap>
          <span>事件详情</span>
          <Typography.Text copyable style={{ fontSize: 12, color: "#6366f1", fontWeight: 600 }}>
            {event.event_id}
          </Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            {formatTime(event.occurred_at || event.received_at)}
          </Typography.Text>
        </Space>
      }
      extra={
        <Space size="small" wrap>
          {event.release && (
            <Tag color="geekblue" style={{ margin: 0, fontSize: 11 }}>
              {event.release}
            </Tag>
          )}
          {event.environment && (
            <Tag color="cyan" style={{ margin: 0, fontSize: 11 }}>
              {event.environment}
            </Tag>
          )}
          {userText && (
            <Space size={2}>
              <UserOutlined style={{ color: "#64748b", fontSize: 11 }} />
              <Typography.Text style={{ fontSize: 11 }}>{userText}</Typography.Text>
            </Space>
          )}
        </Space>
      }
      className="content-card equal-height-card"
    >
      <Tabs defaultActiveKey="exception" size="small" items={tabs} />
    </Card>
  )
}

function ExceptionTab({ event }: { event: Event }) {
  if (!event.exception) {
    return <Empty description="该事件没有记录 Exception 结构化对象" />
  }

  let exceptionObj: Record<string, unknown> | null = null
  if (typeof event.exception === "object") {
    exceptionObj = event.exception as Record<string, unknown>
  } else if (typeof event.exception === "string") {
    try {
      exceptionObj = JSON.parse(event.exception)
    } catch {
      exceptionObj = null
    }
  }

  const tagsList = event.tags ? Object.entries(event.tags) : []

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="small">
      {exceptionObj && (
        <Descriptions bordered size="small" column={{ xs: 1, sm: 2, md: 3 }}>
          <Descriptions.Item label="Type">
            <Typography.Text strong code style={{ color: "#ef4444" }}>
              {String(exceptionObj.type || "Error")}
            </Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="Value" span={2}>
            <Typography.Text strong>{String(exceptionObj.value || event.message || "—")}</Typography.Text>
          </Descriptions.Item>
          {Boolean(event.mechanism) && (
            <Descriptions.Item label="Mechanism" span={3}>
              <pre style={{ margin: 0, fontSize: 11 }}>{JSON.stringify(event.mechanism, null, 2)}</pre>
            </Descriptions.Item>
          )}
        </Descriptions>
      )}

      {tagsList.length > 0 && (
        <div style={{ marginTop: 4 }}>
          <Space wrap size={[4, 4]}>
            {tagsList.map(([k, v]) => (
              <Tag key={k} color="purple" style={{ margin: 0, fontSize: 11, padding: "0 6px" }}>
                <strong>{k}:</strong> {String(v)}
              </Tag>
            ))}
          </Space>
        </div>
      )}
    </Space>
  )
}

function TagsAndContextsTab({ event }: { event: Event }) {
  const hasTags = event.tags && Object.keys(event.tags).length > 0
  const hasContexts = event.contexts && Object.keys(event.contexts).length > 0
  const hasExtra = event.extra && Object.keys(event.extra).length > 0

  if (!hasTags && !hasContexts && !hasExtra) {
    return <Empty description="暂无 Tags 与 Contexts 数据" />
  }

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="small">
      {hasContexts && (
        <Card size="small" title="Contexts (Browser, OS, Runtime...)" type="inner">
          <JsonView value={event.contexts} maxHeight={220} />
        </Card>
      )}

      {hasExtra && (
        <Card size="small" title="Extra Attributes" type="inner">
          <JsonView value={event.extra} maxHeight={220} />
        </Card>
      )}
    </Space>
  )
}

function UserTab({ event }: { event: Event }) {
  if (!event.user) {
    return <Empty description="该事件未附加 User 信息" />
  }

  const user = event.user
  return (
    <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
      <Descriptions.Item label="User ID">
        <Typography.Text code>{user.id || "—"}</Typography.Text>
      </Descriptions.Item>
      <Descriptions.Item label="Username">{user.username || "—"}</Descriptions.Item>
      <Descriptions.Item label="Email">{user.email || "—"}</Descriptions.Item>
      <Descriptions.Item label="IP Address">
        <Tag>{user.ip_address || "—"}</Tag>
      </Descriptions.Item>
      <Descriptions.Item label="Name">{user.name || "—"}</Descriptions.Item>
    </Descriptions>
  )
}

function RequestTab({ event }: { event: Event }) {
  if (!event.request) {
    return <Empty description="该事件未附加 HTTP Request 数据" />
  }

  const req = event.request
  const headersList = req.headers
    ? Object.entries(req.headers).map(([name, value]) => ({ key: name, name, value }))
    : []

  return (
    <Space direction="vertical" style={{ width: "100%" }} size="small">
      <Descriptions bordered size="small" column={{ xs: 1, sm: 2 }}>
        <Descriptions.Item label="Method">
          <Tag color="geekblue">{req.method || "GET"}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="URL">
          <Typography.Text copyable>{req.url || "—"}</Typography.Text>
        </Descriptions.Item>
        {req.query_string && (
          <Descriptions.Item label="Query String" span={2}>
            <Typography.Text code>{req.query_string}</Typography.Text>
          </Descriptions.Item>
        )}
      </Descriptions>

      {headersList.length > 0 && (
        <Card size="small" title="Headers" type="inner">
          <Table
            size="small"
            pagination={false}
            columns={[
              { title: "Header", dataIndex: "name", width: 200, render: (v) => <Typography.Text strong>{v}</Typography.Text> },
              { title: "Value", dataIndex: "value", render: (v) => <Typography.Text code copyable>{v}</Typography.Text> },
            ]}
            dataSource={headersList}
          />
        </Card>
      )}

      {req.data !== undefined && (
        <Card size="small" title="Request Body" type="inner">
          <JsonView value={req.data} maxHeight={220} />
        </Card>
      )}
    </Space>
  )
}
