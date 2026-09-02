import React, { useState } from "react"
import { Button, Card, Descriptions, Empty, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography, message } from "antd"
import { AlertOutlined, DeleteOutlined, EditOutlined, KeyOutlined, PlusOutlined, SettingOutlined, TeamOutlined } from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import { api, type AlertRule, type Project } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"

export function SettingsPage() {
  const { projectId = "" } = useParams()
  const queryClient = useQueryClient()
  const [alertForm] = Form.useForm()
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null)
  const [alertModalOpen, setAlertModalOpen] = useState(false)
  const query = useQuery({
    queryKey: ["project", "default", projectId],
    queryFn: () => api.project("default", projectId),
  })
  const alertQuery = useQuery({ queryKey: ["alert-rules", projectId], queryFn: () => api.alertRules("default", projectId) })
  const saveAlert = useMutation({
    mutationFn: async (values: Record<string, string | number | boolean>) => {
      const data: Partial<AlertRule> = {
        name: String(values.name), condition: values.condition as AlertRule["condition"], threshold: Number(values.threshold || 1), window_minutes: Number(values.window_minutes || 60), cooldown_minutes: Number(values.cooldown_minutes || 30), enabled: Boolean(values.enabled),
        filters: Object.fromEntries([["level", values.level], ["environment", values.environment], ["release", values.release]].filter((entry) => entry[1]).map(([key, value]) => [key, String(value)])),
        actions: [{ type: "webhook", url: String(values.url) }],
      }
      return editingRule ? api.updateAlertRule("default", projectId, editingRule.id, data) : api.createAlertRule("default", projectId, data)
    },
    onSuccess: () => { message.success("告警规则已保存"); setAlertModalOpen(false); setEditingRule(null); alertForm.resetFields(); void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }) },
    onError: (error: Error) => message.error(error.message),
  })
  const deleteAlert = useMutation({ mutationFn: (id: string) => api.deleteAlertRule("default", projectId, id), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }) })
  const toggleAlert = useMutation({ mutationFn: (rule: AlertRule) => api.updateAlertRule("default", projectId, rule.id, { ...rule, enabled: !rule.enabled }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }) })

  if (query.isLoading) return <Loading tip="正在加载项目设置..." />
  if (query.isError) return <ErrorView error={query.error as Error} />

  const project = query.data as Project
  const defaultDsn = project.keys?.[0]?.dsn

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

        <Card title={<Space><AlertOutlined style={{ color: "#6366f1" }} /><span>告警规则 (Alerts)</span></Space>} extra={<Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => { setEditingRule(null); alertForm.resetFields(); alertForm.setFieldsValue({ condition: "count", threshold: 10, window_minutes: 60, cooldown_minutes: 30, enabled: true }); setAlertModalOpen(true) }}>新增规则</Button>} className="content-card">
          <Table size="small" rowKey="id" pagination={false} loading={alertQuery.isLoading} dataSource={alertQuery.data ?? []} columns={[
            { title: "名称", dataIndex: "name" },
            { title: "条件", dataIndex: "condition" },
            { title: "阈值", dataIndex: "threshold", render: (value: number, rule: AlertRule) => rule.condition === "count" ? value : "按 Issue 触发" },
            { title: "过滤器", dataIndex: "filters", render: (filters: Record<string, string> = {}) => <Space wrap>{Object.entries(filters).map(([key, value]) => <Tag key={key}>{key}:{value}</Tag>)}</Space> },
            { title: "启用", dataIndex: "enabled", render: (_: boolean, rule: AlertRule) => <Switch size="small" checked={rule.enabled} loading={toggleAlert.isPending} onChange={() => toggleAlert.mutate(rule)} /> },
            { title: "操作", render: (_: unknown, rule: AlertRule) => <Space><Button size="small" icon={<EditOutlined />} onClick={() => { setEditingRule(rule); alertForm.setFieldsValue({ ...rule, ...rule.filters, url: rule.actions?.[0]?.url }); setAlertModalOpen(true) }}>编辑</Button><Popconfirm title="删除这条告警规则？" onConfirm={() => deleteAlert.mutate(rule.id)}><Button size="small" danger icon={<DeleteOutlined />}>删除</Button></Popconfirm></Space> },
          ]} locale={{ emptyText: "暂无告警规则" }} />
        </Card>

        {/* Embedded SDK QuickStart */}
        <SdkQuickStart dsn={defaultDsn} />
      </Space>
      <Modal title={editingRule ? "编辑告警规则" : "新增告警规则"} open={alertModalOpen} confirmLoading={saveAlert.isPending} onCancel={() => setAlertModalOpen(false)} onOk={() => alertForm.submit()}>
        <Form form={alertForm} layout="vertical" onFinish={(values) => saveAlert.mutate(values)} initialValues={{ condition: "count", threshold: 10, window_minutes: 60, cooldown_minutes: 30, enabled: true }}>
          <Form.Item name="name" label="规则名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="condition" label="触发条件" rules={[{ required: true }]}><Select options={[{ label: "新 Issue", value: "new_issue" }, { label: "回归", value: "regression" }, { label: "窗口事件数", value: "count" }]} /></Form.Item>
          <Form.Item noStyle shouldUpdate={(previous, current) => previous.condition !== current.condition}>{({ getFieldValue }) => <Form.Item name="threshold" label="阈值" extra={getFieldValue("condition") === "count" ? "窗口内事件数达到阈值时触发" : "该条件按 Issue 逐个触发，阈值固定为 1"}><InputNumber min={1} disabled={getFieldValue("condition") !== "count"} style={{ width: "100%" }} /></Form.Item>}</Form.Item>
          <Space style={{ display: "flex" }}><Form.Item name="window_minutes" label="窗口（分钟）"><InputNumber min={1} /></Form.Item><Form.Item name="cooldown_minutes" label="冷却（分钟）"><InputNumber min={0} /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Space>
          <Form.Item name="level" label="Level 过滤"><Select allowClear options={["fatal", "error", "warning", "info"].map((value) => ({ label: value, value }))} /></Form.Item>
          <Form.Item name="environment" label="Environment 过滤"><Input allowClear /></Form.Item>
          <Form.Item name="release" label="Release 过滤"><Input allowClear /></Form.Item>
          <Form.Item name="url" label="Webhook URL" rules={[{ required: true, type: "url" }]}><Input /></Form.Item>
        </Form>
      </Modal>
    </>
  )
}
