import React, { useState, useEffect, useRef } from "react"
import {
  Button,
  Card,
  Empty,
  Flex,
  Radio,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography,
} from "antd"
import {
  CodeOutlined,
  CheckCircleOutlined,
  WarningOutlined,
  DownOutlined,
  RightOutlined,
  CopyOutlined,
  AimOutlined,
} from "@ant-design/icons"
import type { Event, Frame } from "../api"

export function StackTrace({ event }: { event: Event }) {
  const [onlyInApp, setOnlyInApp] = useState(false)
  const hasSymbolicated = (event.symbolicated_frames?.length ?? 0) > 0
  const hasRaw = (event.frames?.length ?? 0) > 0
  const [frameType, setFrameType] = useState<"symbolicated" | "raw">("symbolicated")
  const [expandedSet, setExpandedSet] = useState<Record<number, boolean>>({})
  const sourceFrameRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (hasSymbolicated) {
      setFrameType("symbolicated")
    } else {
      setFrameType("raw")
    }
  }, [hasSymbolicated, event.event_id])

  const activeFrames =
    frameType === "symbolicated" && hasSymbolicated
      ? event.symbolicated_frames!
      : event.frames ?? []

  const filteredFrames = onlyInApp ? activeFrames.filter((f) => f.in_app) : activeFrames
  const status = event.symbolication_status || "not_attempted"

  // Expand in-app frames or frames with context by default
  useEffect(() => {
    const initial: Record<number, boolean> = {}
    filteredFrames.forEach((frame, idx) => {
      if (frame.in_app || frame.context_line) {
        initial[idx] = true
      }
    })
    setExpandedSet(initial)
  }, [filteredFrames.length, frameType, event.event_id])

  const toggleExpand = (idx: number) => {
    setExpandedSet((prev) => ({ ...prev, [idx]: !prev[idx] }))
  }

  const toggleAll = (expand: boolean) => {
    const next: Record<number, boolean> = {}
    filteredFrames.forEach((_, idx) => {
      next[idx] = expand
    })
    setExpandedSet(next)
  }

  const allExpanded = filteredFrames.length > 0 && filteredFrames.every((_, idx) => expandedSet[idx])
  const inAppSourceIndex = filteredFrames.findIndex((frame) => frame.in_app && Boolean(frame.context_line))
  const anySourceIndex = filteredFrames.findIndex((frame) => Boolean(frame.context_line))
  const sourceFrameIndex = inAppSourceIndex >= 0 ? inAppSourceIndex : anySourceIndex

  const locateSourceFrame = () => {
    setExpandedSet((current) => ({ ...current, [sourceFrameIndex]: true }))
    window.requestAnimationFrame(() => {
      sourceFrameRef.current?.scrollIntoView({
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
        block: "center",
      })
      sourceFrameRef.current?.focus({ preventScroll: true })
    })
  }

  return (
    <Card
      title={
        <Space size="middle">
          <Space>
            <CodeOutlined style={{ color: "#6366f1" }} />
            <span>源码调用栈</span>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 12, fontWeight: 400 }}>
            {filteredFrames.length} 帧
          </Typography.Text>
        </Space>
      }
      extra={
        <Space wrap>
          {sourceFrameIndex >= 0 && (
            <Button icon={<AimOutlined />} onClick={locateSourceFrame}>定位报错行</Button>
          )}
          {hasSymbolicated && hasRaw && (
            <Radio.Group
              size="small"
              value={frameType}
              onChange={(e) => setFrameType(e.target.value)}
              buttonStyle="solid"
            >
              <Radio.Button value="symbolicated">反解</Radio.Button>
              <Radio.Button value="raw">原始</Radio.Button>
            </Radio.Group>
          )}

          <Button size="small" onClick={() => toggleAll(!allExpanded)}>
            {allExpanded ? "折叠全部" : "展开全部"}
          </Button>

          <Tooltip title="隐藏系统及 runtime 堆栈帧">
            <Space size={4}>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                仅应用代码:
              </Typography.Text>
              <Switch size="small" checked={onlyInApp} onChange={setOnlyInApp} />
            </Space>
          </Tooltip>

          {status === "symbolicated" ? (
            <Tag color="success" icon={<CheckCircleOutlined />}>
              Source Map 命中
            </Tag>
          ) : status === "miss" ? (
            <Tag color="warning" icon={<WarningOutlined />}>
              Source Map 未匹配
            </Tag>
          ) : (
            <Tag>{status}</Tag>
          )}
        </Space>
      }
      className="content-card stack-card"
      styles={{ body: { padding: 0 } }}
    >
      {filteredFrames.length === 0 ? (
        <div style={{ padding: 32 }}>
          <Empty description={onlyInApp ? "没有匹配的应用代码帧 (已过滤第三方库)" : "该事件没有 Stack Trace 数据"} />
        </div>
      ) : (
        <div className="sentry-stacktrace-tree">
          {filteredFrames.map((frame, index) => {
            const isExpanded = Boolean(expandedSet[index])
            const hasContext = Boolean(frame.context_line || frame.pre_context?.length || frame.post_context?.length)
            const targetLineNum = frame.lineno ?? 0
            const preLines = frame.pre_context ?? []
            const postLines = frame.post_context ?? []

            return (
              <div
                key={`${frame.filename}-${frame.lineno}-${frame.colno}-${index}`}
                className={`sentry-frame-node ${frame.in_app ? "is-in-app" : "is-library"}`}
                ref={index === sourceFrameIndex ? sourceFrameRef : undefined}
                tabIndex={index === sourceFrameIndex ? -1 : undefined}
              >
                {/* Unified Frame Header Row */}
                <button
                  type="button"
                  className={`sentry-frame-header ${hasContext ? "clickable" : ""}`}
                  onClick={() => hasContext && toggleExpand(index)}
                  disabled={!hasContext}
                  aria-expanded={hasContext ? isExpanded : undefined}
                >
                  <Flex align="center" justify="space-between" style={{ width: "100%" }}>
                    <Space size="middle">
                      {hasContext ? (
                        <span className="sentry-expand-icon">
                          {isExpanded ? <DownOutlined /> : <RightOutlined />}
                        </span>
                      ) : (
                        <span className="sentry-expand-icon placeholder" />
                      )}

                      <div className="sentry-frame-titles">
                        <span className="sentry-frame-func">
                          {frame.function || "<anonymous>"}
                        </span>
                        <span className="sentry-frame-file">
                          in <strong>{frame.filename || "unknown"}</strong> at line{" "}
                          <span className="sentry-frame-loc-tag">
                            {frame.lineno ?? "?"}:{frame.colno ?? "?"}
                          </span>
                        </span>
                      </div>
                    </Space>

                    <Space size="small">
                      <Tag
                        color={frame.in_app ? "indigo" : "default"}
                        style={{ margin: 0, fontSize: 11, fontWeight: 600 }}
                      >
                        {frame.in_app ? "app" : "system"}
                      </Tag>
                    </Space>
                  </Flex>
                </button>

                {/* Seamless Embedded Code Viewer */}
                {isExpanded && hasContext && (
                  <div className="sentry-code-block">
                    {preLines.map((line, pIdx) => {
                      const lineNum = targetLineNum - preLines.length + pIdx
                      return (
                        <div className="sentry-code-line pre" key={`pre-${pIdx}`}>
                          <span className="sentry-line-no">{lineNum}</span>
                          <span className="sentry-line-gutter" />
                          <span className="sentry-line-code">{line || " "}</span>
                        </div>
                      )
                    })}

                    {frame.context_line && (
                      <div className="sentry-code-line target">
                        <span className="sentry-line-no">{targetLineNum}</span>
                        <span className="sentry-line-gutter">»</span>
                        <span className="sentry-line-code">{frame.context_line}</span>
                      </div>
                    )}

                    {postLines.map((line, pIdx) => {
                      const lineNum = targetLineNum + 1 + pIdx
                      return (
                        <div className="sentry-code-line post" key={`post-${pIdx}`}>
                          <span className="sentry-line-no">{lineNum}</span>
                          <span className="sentry-line-gutter" />
                          <span className="sentry-line-code">{line || " "}</span>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}
