import React from "react"
import { Empty, Tag, Timeline, Typography, Collapse } from "antd"
import {
  InteractionOutlined,
  ApiOutlined,
  CompassOutlined,
  CodeOutlined,
  BugOutlined,
  InfoCircleOutlined,
} from "@ant-design/icons"
import type { Breadcrumb } from "../api"
import { formatTime, levelColor } from "./Common"

function getCategoryIcon(category?: string, type?: string) {
  const c = (category || type || "").toLowerCase()
  if (c.includes("ui") || c.includes("click") || c.includes("input")) {
    return <InteractionOutlined style={{ color: "#6366f1" }} />
  }
  if (c.includes("http") || c.includes("xhr") || c.includes("fetch") || c.includes("api")) {
    return <ApiOutlined style={{ color: "#0ea5e9" }} />
  }
  if (c.includes("nav") || c.includes("route") || c.includes("url")) {
    return <CompassOutlined style={{ color: "#10b981" }} />
  }
  if (c.includes("console") || c.includes("log")) {
    return <CodeOutlined style={{ color: "#f59e0b" }} />
  }
  if (c.includes("error") || c.includes("exception")) {
    return <BugOutlined style={{ color: "#ef4444" }} />
  }
  return <InfoCircleOutlined style={{ color: "#64748b" }} />
}

export function BreadcrumbsTimeline({ breadcrumbs }: { breadcrumbs?: Breadcrumb[] }) {
  if (!breadcrumbs || breadcrumbs.length === 0) {
    return <Empty description="该事件没有记录面包屑 (Breadcrumbs)" />
  }

  const items = breadcrumbs.map((crumb, idx) => {
    const hasData = crumb.data && Object.keys(crumb.data).length > 0
    return {
      dot: getCategoryIcon(crumb.category, crumb.type),
      children: (
        <div className="breadcrumb-item" key={idx}>
          <div className="breadcrumb-header">
            {crumb.category && <Tag color="blue">{crumb.category}</Tag>}
            {crumb.level && <Tag color={levelColor(crumb.level)}>{crumb.level}</Tag>}
            <Typography.Text type="secondary" className="breadcrumb-meta">
              {formatTime(crumb.timestamp)}
            </Typography.Text>
          </div>
          {crumb.message && (
            <Typography.Text strong className="breadcrumb-msg">
              {crumb.message}
            </Typography.Text>
          )}
          {hasData && (
            <Collapse
              ghost
              size="small"
              items={[
                {
                  key: "data",
                  label: <Typography.Text type="secondary" style={{ fontSize: 12 }}>详细参数 (Data)</Typography.Text>,
                  children: (
                    <pre style={{ margin: 0, padding: 8, background: "#f1f5f9", borderRadius: 4, fontSize: 11 }}>
                      {JSON.stringify(crumb.data, null, 2)}
                    </pre>
                  ),
                },
              ]}
            />
          )}
        </div>
      ),
    }
  })

  return <Timeline items={items} />
}
