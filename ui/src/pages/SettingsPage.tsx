import React from "react"
import { Button, Card, Descriptions, Empty, Form, Input, InputNumber, Space, Table, Tag, Typography } from "antd"
import { AlertOutlined, KeyOutlined, SettingOutlined, TeamOutlined } from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import { api, type Project } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"

export function SettingsPage() {
  const { projectId = "" } = useParams()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ["project", "default", projectId],
    queryFn: () => api.project("default", projectId),
  })

  if (query.isLoading) return <Loading tip="正在加载项目设置..." />
  if (query.isError) return <ErrorView error={query.error as Error} />

  const project = query.data as Project
  const defaultDsn = project.keys?.[0]?.dsn
  const alertQuery = useQuery({ queryKey: ["alert-rules", projectId], queryFn: () => api.alertRules("default", projectId) })
  const alertMutation = useMutation({
    mutationFn: (value: { name: string; threshold: number; window_minutes: number; url: string }) => api.createAlertRule("default", projectId, { ...value, condition: "count", enabled: true, cooldown_minutes: 30, actions: [{ type: "webhook", url: value.url }] }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }),
  })

  return (
    <>
      <PageHeader title="Project Settings" subtitle="项目配置、访问凭据与 SDK 接入方式" />

      <Space orientation="vertical" size="large" style={{ width: "100%" }}>
        {/* Project Info Card */}
        <Card
          title={
            <Space>
              <SettingOutlined style={{ color: "#6366f1" }} />
              <span>基本信息 (General)</span>
            </Space>
          }
          className="content-card"
        >
          <Descriptions bordered size="small" column={{ xs: 1, sm: 2, md: 3 }}>
            <Descriptions.Item label="项目名称">
              <Typography.Text strong>{project.name}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="项目 Slug">
              <Typography.Text code>{project.slug}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="所属平台">
              <Tag color="indigo">{project.platform || "javascript"}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="创建时间">{formatTime(project.dateCreated)}</Descriptions.Item>
          </Descriptions>
        </Card>

        {/* Project Keys (DSN) Card */}
        <Card
          title={
            <Space>
              <KeyOutlined style={{ color: "#6366f1" }} />
              <span>Client Keys (DSN)</span>
            </Space>
          }
          className="content-card"
        >
          <Table
            rowKey="id"
            pagination={false}
            columns={[
              { title: "Key 名称", dataIndex: "name", width: 140, render: (v) => <Typography.Text strong>{v || "Default"}</Typography.Text> },
              {
                title: "Public Key",
                dataIndex: "public",
                width: 220,
                render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text>,
              },
              {
                title: "DSN (客户端接入配置)",
                dataIndex: "dsn",
                render: (value: string) => (
                  <Typography.Text code copyable ellipsis style={{ color: "#6366f1", fontWeight: 600 }}>
                    {value}
                  </Typography.Text>
                ),
              },
            ]}
            dataSource={project.keys ?? []}
            locale={{ emptyText: <Empty description="暂无 Project Key" /> }}
          />
        </Card>

        {/* Teams Card */}
        <Card
          title={
            <Space>
              <TeamOutlined style={{ color: "#6366f1" }} />
              <span>关联团队 (Teams)</span>
            </Space>
          }
          className="content-card"
        >
          {(project.teams ?? []).length > 0 ? (
            <Space wrap>
              {project.teams!.map((team) => (
                <Tag key={team.id} color="blue" style={{ fontSize: 13, padding: "4px 10px" }}>
                  {team.name} ({team.slug})
                </Tag>
              ))}
            </Space>
          ) : (
            <Typography.Text type="secondary">当前项目未关联特定团队，默认组织成员均可访问。</Typography.Text>
          )}
        </Card>

        <Card title={<Space><AlertOutlined style={{ color: "#6366f1" }} /><span>告警规则 (Alerts)</span></Space>} className="content-card">
          <Form layout="inline" onFinish={(values) => alertMutation.mutate(values as { name: string; threshold: number; window_minutes: number; url: string })}>
            <Form.Item name="name" rules={[{ required: true }]}><Input size="small" placeholder="规则名称" /></Form.Item>
            <Form.Item name="threshold" initialValue={10}><InputNumber size="small" min={1} placeholder="阈值" /></Form.Item>
            <Form.Item name="window_minutes" initialValue={60}><InputNumber size="small" min={1} placeholder="窗口(分钟)" /></Form.Item>
            <Form.Item name="url" rules={[{ required: true, type: "url" }]}><Input size="small" placeholder="Webhook URL" style={{ width: 220 }} /></Form.Item>
            <Form.Item><Button size="small" type="primary" htmlType="submit" loading={alertMutation.isPending}>新增</Button></Form.Item>
          </Form>
          <Table size="small" rowKey="id" pagination={false} style={{ marginTop: 12 }} dataSource={alertQuery.data ?? []} columns={[{ title: "名称", dataIndex: "name" }, { title: "条件", dataIndex: "condition" }, { title: "阈值", dataIndex: "threshold" }, { title: "状态", dataIndex: "enabled", render: (v: boolean) => <Tag color={v ? "green" : "default"}>{v ? "启用" : "停用"}</Tag> }]} locale={{ emptyText: "暂无告警规则" }} />
        </Card>

        {/* Embedded SDK QuickStart */}
        <SdkQuickStart dsn={defaultDsn} />
      </Space>
    </>
  )
}
