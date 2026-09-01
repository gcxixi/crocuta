import React, { useState } from "react"
import { Button, Card, Space, Tabs, Typography, message } from "antd"
import { CopyOutlined, CheckOutlined, RocketOutlined } from "@ant-design/icons"

export function SdkQuickStart({ dsn }: { dsn?: string }) {
  const [copied, setCopied] = useState(false)
  const activeDsn = dsn || "http://public@localhost:8081/1"

  const handleCopy = (text: string) => {
    navigator.clipboard.writeText(text)
    setCopied(true)
    message.success("接入代码已复制到剪贴板")
    setTimeout(() => setCopied(false), 2000)
  }

  const reactSnippet = `import * as Sentry from "@sentry/react";

Sentry.init({
  dsn: "${activeDsn}",
  integrations: [Sentry.browserTracingIntegration()],
  tracesSampleRate: 1.0,
});`

  const vueSnippet = `import * as Sentry from "@sentry/vue";

Sentry.init({
  app,
  dsn: "${activeDsn}",
  integrations: [Sentry.browserTracingIntegration()],
  tracesSampleRate: 1.0,
});`

  const nodeSnippet = `import * as Sentry from "@sentry/node";

Sentry.init({
  dsn: "${activeDsn}",
  tracesSampleRate: 1.0,
});`

  const browserSnippet = `import * as Sentry from "@sentry/browser";

Sentry.init({
  dsn: "${activeDsn}",
  tracesSampleRate: 1.0,
});`

  const angularSnippet = `import * as Sentry from "@sentry/angular";

Sentry.init({
  dsn: "${activeDsn}",
  tracesSampleRate: 1.0,
});`

  const items = [
    { key: "react", label: "React", snippet: reactSnippet },
    { key: "vue", label: "Vue.js", snippet: vueSnippet },
    { key: "node", label: "Node.js", snippet: nodeSnippet },
    { key: "browser", label: "Browser / TS", snippet: browserSnippet },
    { key: "angular", label: "Angular", snippet: angularSnippet },
  ]

  return (
    <Card
      title={
        <Space>
          <RocketOutlined style={{ color: "#6366f1" }} />
          <span>SDK 快速接入指引</span>
        </Space>
      }
      className="content-card"
    >
      <Tabs
        defaultActiveKey="react"
        items={items.map((tab) => ({
          key: tab.key,
          label: tab.label,
          children: (
            <div>
              <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                <Button
                  size="small"
                  icon={copied ? <CheckOutlined /> : <CopyOutlined />}
                  onClick={() => handleCopy(tab.snippet)}
                >
                  {copied ? "已复制" : "复制代码"}
                </Button>
              </div>
              <pre className="sdk-box">
                <code>{tab.snippet}</code>
              </pre>
            </div>
          ),
        }))}
      />
    </Card>
  )
}
