import React, { useState } from "react"
import {
  Button,
  Card,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd"
import { PlusOutlined, FileZipOutlined, ReloadOutlined, DeleteOutlined } from "@ant-design/icons"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "react-router-dom"
import type { ColumnsType } from "antd/es/table"
import { api, type Release, type ArtifactInfo } from "../api"
import { ErrorView, Loading, PageHeader, formatTime } from "../components/Common"

export function ReleasesPage() {
  const { projectId = "" } = useParams()
  const client = useQueryClient()
  const [openCreate, setOpenCreate] = useState(false)
  const [activeRelease, setActiveRelease] = useState<string | null>(null)
  const [form] = Form.useForm<{ version: string }>()

  const query = useQuery({
    queryKey: ["releases", projectId],
    queryFn: () => api.releases(projectId),
  })

  const createMutation = useMutation({
    mutationFn: (version: string) => api.createRelease(projectId, version),
    onSuccess: () => {
      setOpenCreate(false)
      form.resetFields()
      message.success("Release 创建成功")
      void client.invalidateQueries({ queryKey: ["releases", projectId] })
    },
    onError: (err: Error) => {
      message.error(`创建失败: ${err.message}`)
    },
  })

  const artifactsQuery = useQuery({
    queryKey: ["artifacts", projectId, activeRelease],
    queryFn: () => (activeRelease ? api.artifacts(projectId, activeRelease) : Promise.resolve([])),
    enabled: !!activeRelease,
  })

  const deleteArtifactMutation = useMutation({
    mutationFn: ({ name, dist }: { name: string; dist?: string }) =>
      api.deleteArtifact(projectId, activeRelease!, name, dist),
    onSuccess: () => {
      message.success("Artifact 删除成功")
      void client.invalidateQueries({ queryKey: ["artifacts", projectId, activeRelease] })
    },
    onError: (err: Error) => {
      message.error(`删除失败: ${err.message}`)
    },
  })

  if (query.isLoading) return <Loading tip="正在加载 Releases..." />
  if (query.isError) return <ErrorView error={query.error as Error} retry={() => void query.refetch()} />

  const columns: ColumnsType<Release> = [
    {
      title: "Version",
      dataIndex: "version",
      render: (value: string) => (
        <Typography.Text code strong style={{ fontSize: 13, color: "#6366f1" }}>
          {value}
        </Typography.Text>
      ),
    },
    {
      title: "创建时间",
      dataIndex: "created_at",
      render: formatTime,
    },
    {
      title: "操作",
      key: "actions",
      width: 150,
      render: (_, row) => (
        <Button
          size="small"
          icon={<FileZipOutlined />}
          onClick={() => setActiveRelease(row.version)}
        >
          查看 Artifacts
        </Button>
      ),
    },
  ]

  const artifactColumns: ColumnsType<ArtifactInfo> = [
    {
      title: "Artifact Name",
      dataIndex: "name",
      render: (value: string) => <Typography.Text code>{value}</Typography.Text>,
    },
    {
      title: "Debug ID",
      dataIndex: "debug_id",
      render: (v?: string) => (v ? <Tag color="cyan">{v}</Tag> : <Typography.Text type="secondary">—</Typography.Text>),
    },
    {
      title: "Dist",
      dataIndex: "dist",
      render: (v?: string) => v || "—",
    },
    {
      title: "大小",
      dataIndex: "size",
      render: (v?: number) => (v ? `${(v / 1024).toFixed(1)} KB` : "—"),
    },
    {
      title: "操作",
      key: "del",
      render: (_, item) => (
        <Popconfirm
          title="确认删除该 Artifact?"
          onConfirm={() => deleteArtifactMutation.mutate({ name: item.name, dist: item.dist })}
        >
          <Button size="small" danger icon={<DeleteOutlined />} />
        </Popconfirm>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title="Releases"
        subtitle="版本发布与 Source Map 产物管理"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>
              刷新
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpenCreate(true)}>
              创建 Release
            </Button>
          </Space>
        }
      />

      <Card className="content-card">
        <Table
          rowKey={(row) => `${row.project_id}-${row.version}`}
          columns={columns}
          dataSource={query.data ?? []}
          locale={{ emptyText: <Empty description="暂无 Release 记录，可通过 CLI 或右上角创建" /> }}
        />
      </Card>

      {/* Create Release Modal */}
      <Modal
        title="创建新 Release"
        open={openCreate}
        confirmLoading={createMutation.isPending}
        onCancel={() => setOpenCreate(false)}
        onOk={() => {
          form.validateFields().then((vals) => {
            createMutation.mutate(vals.version.trim())
          })
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="version"
            label="Release 版本号"
            rules={[{ required: true, message: "请输入版本号，例如 web@1.0.0" }]}
          >
            <Input placeholder="例如: web@1.0.0 或 sentryx-app@2.1.0" />
          </Form.Item>
        </Form>
      </Modal>

      {/* Artifacts Drawer */}
      <Drawer
        title={
          <Space>
            <FileZipOutlined />
            <span>Artifacts - {activeRelease}</span>
          </Space>
        }
        width={680}
        open={!!activeRelease}
        onClose={() => setActiveRelease(null)}
        extra={
          <Button
            size="small"
            icon={<ReloadOutlined />}
            onClick={() => void artifactsQuery.refetch()}
            loading={artifactsQuery.isFetching}
          >
            刷新
          </Button>
        }
      >
        {artifactsQuery.isLoading ? (
          <Loading tip="加载 Artifacts..." />
        ) : (
          <Table
            size="small"
            rowKey={(r) => `${r.name}-${r.dist || ""}`}
            columns={artifactColumns}
            dataSource={artifactsQuery.data ?? []}
            locale={{ emptyText: <Empty description="该 Release 暂无上传的 Source Map / Artifact" /> }}
          />
        )}
      </Drawer>
    </>
  )
}
