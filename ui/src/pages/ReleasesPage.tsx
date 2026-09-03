import React, { useState } from "react"
import { App, Button, Card, Empty, Form, Input, Modal, Popconfirm, Space, Table, Tag, Typography } from "antd"
import { DeleteOutlined, PlusOutlined, ReloadOutlined } from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type ArtifactInfo, type Release } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"

function ReleaseArtifacts({ projectId, release }: { projectId: string; release: string }) {
  const { message } = App.useApp()
  const client = useQueryClient()
  const query = useQuery({ queryKey: ["artifacts", projectId, release], queryFn: () => api.artifacts(projectId, release) })
  const remove = useMutation({
    mutationFn: ({ name, dist }: { name: string; dist?: string }) => api.deleteArtifact(projectId, release, name, dist),
    onSuccess: () => { message.success("Source Map 已删除"); void client.invalidateQueries({ queryKey: ["artifacts", projectId, release] }) },
    onError: (error: Error) => message.error(`删除失败：${error.message}`),
  })
  const columns: ColumnsType<ArtifactInfo> = [
    { title: "Source Map / Artifact", dataIndex: "name", render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
    { title: "Debug ID", dataIndex: "debug_id", render: (value?: string) => value ? <Typography.Text code copyable>{value}</Typography.Text> : "—" },
    { title: "Dist", dataIndex: "dist", width: 100, render: (value?: string) => value || "—" },
    { title: "大小", dataIndex: "size", width: 100, align: "right", render: (value?: number) => value == null ? "—" : `${(value / 1024).toFixed(1)} KB` },
    { title: "上传时间", dataIndex: "created_at", width: 150, render: formatTime },
    { title: "操作", width: 60, render: (_value, item) => <Popconfirm title="删除这个 Source Map？" description="删除后无法用于后续堆栈反解。" onConfirm={() => remove.mutate({ name: item.name, dist: item.dist })}><Button danger type="text" icon={<DeleteOutlined />} aria-label={`删除 ${item.name}`} /></Popconfirm> },
  ]
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />
  return <div className="release-artifacts"><Table loading={query.isLoading} rowKey={(item) => `${item.name}-${item.dist || ""}`} columns={columns} dataSource={query.data ?? []} pagination={false} scroll={{ x: 760 }} locale={{ emptyText: <Empty description="该 Release 暂无 Source Map" /> }} /></div>
}

export function ReleasesPage() {
  const { message } = App.useApp()
  const { projectId = "" } = useParams()
  const client = useQueryClient()
  const [openCreate, setOpenCreate] = useState(false)
  const [form] = Form.useForm<{ version: string }>()
  const query = useQuery({ queryKey: ["releases", projectId], queryFn: () => api.releases(projectId) })
  const createMutation = useMutation({
    mutationFn: (version: string) => api.createRelease(projectId, version),
    onSuccess: () => { setOpenCreate(false); form.resetFields(); message.success("Release 已创建"); void client.invalidateQueries({ queryKey: ["releases", projectId] }) },
    onError: (error: Error) => message.error(`创建失败：${error.message}`),
  })
  if (query.isLoading) return <Loading tip="正在加载 Releases..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />
  const columns: ColumnsType<Release> = [
    { title: "版本", dataIndex: "version", render: (value: string) => <Typography.Text code strong className="release-version">{value}</Typography.Text> },
    { title: "创建时间", dataIndex: "created_at", width: 180, render: formatTime },
    { title: "产物", width: 160, render: () => <Tag>展开查看 Source Map</Tag> },
  ]

  return <>
    <PageHeader title="Releases" subtitle="版本发布与 Source Map 产物管理" extra={<Space><Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={() => setOpenCreate(true)}>创建 Release</Button></Space>} />
    <Card className="content-card release-table-card" styles={{ body: { padding: 0 } }}>
      <Table rowKey={(row) => `${row.project_id}-${row.version}`} columns={columns} dataSource={query.data ?? []} expandable={{ expandedRowRender: (row) => <ReleaseArtifacts projectId={projectId} release={row.version} />, rowExpandable: () => true, columnTitle: "Source Maps", columnWidth: 104 }} locale={{ emptyText: <Empty description="暂无 Release，可创建版本后上传 Source Map" /> }} />
    </Card>
    <Modal title="创建 Release" open={openCreate} confirmLoading={createMutation.isPending} onCancel={() => setOpenCreate(false)} onOk={() => void form.validateFields().then((values) => createMutation.mutate(values.version.trim()))} okText="创建">
      <Form form={form} layout="vertical"><Form.Item name="version" label="Release 版本号" rules={[{ required: true, message: "请输入版本号，例如 web@1.0.0" }]}><Input placeholder="例如：web@1.0.0" /></Form.Item></Form>
    </Modal>
  </>
}
