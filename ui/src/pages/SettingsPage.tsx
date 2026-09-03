import React, { useState } from "react"
import { App, Button, Card, Descriptions, Empty, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tabs, Tag, Typography } from "antd"
import { AlertOutlined, DeleteOutlined, EditOutlined, KeyOutlined, PlusOutlined, SettingOutlined, TeamOutlined } from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import { api, type AlertRule, type Project } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"
import { SdkQuickStart } from "../components/SdkQuickStart"

export function SettingsPage() {
  const { message } = App.useApp()
  const { projectId = "" } = useParams()
  const queryClient = useQueryClient()
  const [alertForm] = Form.useForm()
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null)
  const [alertModalOpen, setAlertModalOpen] = useState(false)
  const query = useQuery({ queryKey: ["project", "default", projectId], queryFn: () => api.project("default", projectId) })
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
    onError: (error: Error) => message.error(`保存失败：${error.message}`),
  })
  const deleteAlert = useMutation({ mutationFn: (id: string) => api.deleteAlertRule("default", projectId, id), onSuccess: () => { message.success("告警规则已删除"); void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }) }, onError: (error: Error) => message.error(`删除失败：${error.message}`) })
  const toggleAlert = useMutation({ mutationFn: (rule: AlertRule) => api.updateAlertRule("default", projectId, rule.id, { ...rule, enabled: !rule.enabled }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["alert-rules", projectId] }), onError: (error: Error) => message.error(`状态更新失败：${error.message}`) })

  if (query.isLoading) return <Loading tip="正在加载项目设置..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />
  const project = query.data as Project
  const openNewAlert = () => {
    setEditingRule(null)
    alertForm.resetFields()
    alertForm.setFieldsValue({ condition: "count", threshold: 10, window_minutes: 60, cooldown_minutes: 30, enabled: true })
    setAlertModalOpen(true)
  }

  const credentials = <Space orientation="vertical" size={10} className="settings-stack">
    <Card size="small" title={<Space><KeyOutlined /><span>客户端密钥与 DSN</span></Space>} className="content-card">
      <Table rowKey="id" pagination={false} scroll={{ x: 720 }} columns={[
        { title: "名称", dataIndex: "name", width: 140, render: (value) => <Typography.Text strong>{value || "默认密钥"}</Typography.Text> },
        { title: "Public Key", dataIndex: "public", width: 230, render: (value: string) => <Typography.Text code copyable>{value}</Typography.Text> },
        { title: "客户端 DSN", dataIndex: "dsn", render: (value: string) => <Typography.Text code copyable ellipsis className="dsn-value">{value}</Typography.Text> },
      ]} dataSource={project.keys ?? []} locale={{ emptyText: <Empty description="暂无项目密钥" /> }} />
    </Card>
    <SdkQuickStart dsn={project.keys?.[0]?.dsn} />
  </Space>

  const alerts = <Card size="small" className="content-card" title={<Space><AlertOutlined /><span>告警规则</span></Space>} extra={<Button type="primary" icon={<PlusOutlined />} onClick={openNewAlert}>新增规则</Button>}>
    <Table rowKey="id" pagination={false} loading={alertQuery.isLoading} dataSource={alertQuery.data ?? []} scroll={{ x: 760 }} columns={[
      { title: "名称", dataIndex: "name" },
      { title: "条件", dataIndex: "condition", render: (value: AlertRule["condition"]) => ({ new_issue: "新 Issue", regression: "回归", count: "窗口事件数" }[value]) },
      { title: "阈值", dataIndex: "threshold", align: "right" as const, render: (value: number, rule: AlertRule) => rule.condition === "count" ? value : "逐条触发" },
      { title: "过滤器", dataIndex: "filters", render: (filters: Record<string, string> = {}) => <Space wrap>{Object.entries(filters).map(([key, value]) => <Tag key={key}>{key}:{value}</Tag>)}</Space> },
      { title: "启用", dataIndex: "enabled", width: 70, render: (_: boolean, rule: AlertRule) => <Switch checked={rule.enabled} loading={toggleAlert.isPending} onChange={() => toggleAlert.mutate(rule)} aria-label={`${rule.enabled ? "停用" : "启用"} ${rule.name}`} /> },
      { title: "操作", width: 150, render: (_: unknown, rule: AlertRule) => <Space><Button icon={<EditOutlined />} onClick={() => { setEditingRule(rule); alertForm.setFieldsValue({ ...rule, ...rule.filters, url: rule.actions?.[0]?.url }); setAlertModalOpen(true) }}>编辑</Button><Popconfirm title="删除这条告警规则？" description="删除后无法恢复。" onConfirm={() => deleteAlert.mutate(rule.id)}><Button danger icon={<DeleteOutlined />}>删除</Button></Popconfirm></Space> },
    ]} locale={{ emptyText: <Empty description="暂无告警规则，可新建一条开始接收通知" /> }} />
  </Card>

  const general = <Space orientation="vertical" size={10} className="settings-stack">
    <Card size="small" title={<Space><SettingOutlined /><span>基本信息</span></Space>} className="content-card">
      <Descriptions bordered column={{ xs: 1, sm: 2, md: 3 }}>
        <Descriptions.Item label="项目名称"><Typography.Text strong>{project.name}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="项目 Slug"><Typography.Text code>{project.slug}</Typography.Text></Descriptions.Item>
        <Descriptions.Item label="平台"><Tag color="blue">{project.platform || "javascript"}</Tag></Descriptions.Item>
        <Descriptions.Item label="创建时间">{formatTime(project.dateCreated)}</Descriptions.Item>
      </Descriptions>
    </Card>
    <Card size="small" title={<Space><TeamOutlined /><span>关联团队</span></Space>} className="content-card">
      {(project.teams ?? []).length > 0 ? <Space wrap>{project.teams!.map((team) => <Tag key={team.id}>{team.name} · {team.slug}</Tag>)}</Space> : <Typography.Text type="secondary">当前项目未关联特定团队，默认组织成员均可访问。</Typography.Text>}
    </Card>
  </Space>

  return <>
    <PageHeader title="项目设置" subtitle="管理接入凭据、通知规则与项目属性" />
    <Card className="content-card settings-tabs-card" styles={{ body: { paddingTop: 0 } }}>
      <Tabs items={[
        { key: "credentials", label: <Space><KeyOutlined />凭据与 SDK</Space>, children: credentials },
        { key: "alerts", label: <Space><AlertOutlined />告警规则</Space>, children: alerts },
        { key: "general", label: <Space><SettingOutlined />基本信息</Space>, children: general },
      ]} />
    </Card>
    <Modal title={editingRule ? "编辑告警规则" : "新增告警规则"} open={alertModalOpen} confirmLoading={saveAlert.isPending} onCancel={() => setAlertModalOpen(false)} onOk={() => alertForm.submit()} okText="保存规则">
      <Form form={alertForm} layout="vertical" onFinish={(values) => saveAlert.mutate(values)} initialValues={{ condition: "count", threshold: 10, window_minutes: 60, cooldown_minutes: 30, enabled: true }}>
        <Form.Item name="name" label="规则名称" rules={[{ required: true, message: "请输入规则名称" }]}><Input placeholder="例如：生产环境错误激增" /></Form.Item>
        <Form.Item name="condition" label="触发条件" rules={[{ required: true }]}><Select options={[{ label: "新 Issue", value: "new_issue" }, { label: "回归", value: "regression" }, { label: "窗口事件数", value: "count" }]} /></Form.Item>
        <Form.Item noStyle shouldUpdate={(previous, current) => previous.condition !== current.condition}>{({ getFieldValue }) => <Form.Item name="threshold" label="阈值" extra={getFieldValue("condition") === "count" ? "窗口内事件数达到阈值时触发" : "该条件按 Issue 逐个触发，阈值固定为 1"}><InputNumber min={1} disabled={getFieldValue("condition") !== "count"} className="full-width" /></Form.Item>}</Form.Item>
        <Space className="alert-timing-fields"><Form.Item name="window_minutes" label="窗口（分钟）"><InputNumber min={1} /></Form.Item><Form.Item name="cooldown_minutes" label="冷却（分钟）"><InputNumber min={0} /></Form.Item><Form.Item name="enabled" label="启用" valuePropName="checked"><Switch /></Form.Item></Space>
        <Form.Item name="level" label="级别过滤"><Select allowClear options={["fatal", "error", "warning", "info"].map((value) => ({ label: value, value }))} /></Form.Item>
        <Form.Item name="environment" label="环境过滤"><Input allowClear /></Form.Item>
        <Form.Item name="release" label="Release 过滤"><Input allowClear /></Form.Item>
        <Form.Item name="url" label="Webhook URL" rules={[{ required: true, type: "url", message: "请输入有效的 Webhook URL" }]}><Input /></Form.Item>
      </Form>
    </Modal>
  </>
}
