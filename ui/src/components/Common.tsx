import React from "react"
import { Alert, Breadcrumb, Button, Flex, Spin, Tag, Typography, message } from "antd"
import { CopyOutlined, CheckOutlined } from "@ant-design/icons"

export function formatTime(value?: string) {
  if (!value) return "—"
  try {
    const d = new Date(value)
    return d.toLocaleString()
  } catch {
    return value
  }
}

export function levelColor(level?: string) {
  switch (level?.toLowerCase()) {
    case "fatal":
      return "magenta"
    case "error":
      return "red"
    case "warning":
      return "orange"
    case "info":
      return "blue"
    case "debug":
      return "default"
    default:
      return "red"
  }
}

export function ErrorView({ error, retry }: { error: Error; retry?: () => void }) {
  return (
    <Alert
      type="error"
      showIcon
      message="请求遇到错误"
      description={error.message}
      action={retry ? <Button size="small" type="primary" danger onClick={retry}>重试</Button> : undefined}
      className="mb16"
    />
  )
}

export function Loading({ tip = "加载中..." }: { tip?: string }) {
  return (
    <Flex justify="center" align="center" className="loading">
      <Spin size="large" tip={tip} />
    </Flex>
  )
}

export function JsonView({ value, maxHeight = 420 }: { value: unknown; maxHeight?: number }) {
  const [copied, setCopied] = React.useState(false)
  const jsonStr = JSON.stringify(value ?? {}, null, 2)

  const handleCopy = () => {
    navigator.clipboard.writeText(jsonStr)
    setCopied(true)
    message.success("已复制到剪贴板")
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="json-view-container">
      <Button
        size="small"
        icon={copied ? <CheckOutlined /> : <CopyOutlined />}
        onClick={handleCopy}
        className="json-view-copy"
        type="text"
        style={{ color: "#94a3b8" }}
      >
        {copied ? "已复制" : "复制"}
      </Button>
      <pre className="json-view" style={{ maxHeight }}>
        {jsonStr}
      </pre>
    </div>
  )
}

export function PageHeader({
  title,
  subtitle,
  breadcrumbItems = [],
  breadcrumbTitle,
  extra,
}: {
  title: string
  subtitle?: string
  breadcrumbItems?: { title: React.ReactNode }[]
  breadcrumbTitle?: React.ReactNode
  extra?: React.ReactNode
}) {
  const lastTitle = breadcrumbTitle ?? title
  const allBreadcrumbs = [{ title: "SentryX" }, ...breadcrumbItems, { title: lastTitle }]
  return (
    <Flex justify="space-between" align="center" className="page-header">
      <div>
        <Breadcrumb items={allBreadcrumbs} />
        <Typography.Title level={2} style={{ marginBottom: 0 }}>
          {title}
        </Typography.Title>
        {subtitle && (
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {subtitle}
          </Typography.Text>
        )}
      </div>
      {extra && <div>{extra}</div>}
    </Flex>
  )
}
