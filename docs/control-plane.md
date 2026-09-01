# Sentry 兼容控制面与渐进式迁移

SentryX 复用官方 SDK 与 Envelope/Store/Release/Source Map 协议，不创建私有上报协议。控制面提供 Sentry 常用的 Organization、User、Team、Project、Project Key 资源，使现有 SDK 只需切换 DSN，管理脚本可以按阶段迁移。

## 双写

Relay 使用 `SENTRYX_MIRROR_URL` 配置第二个 Sentry 兼容入口。Envelope 和 Store 请求先同步转发到主上游，再异步复制到 mirror；mirror 失败不会影响主请求响应，日志记录失败原因。可用 `SENTRYX_MIRROR_RELAY_TOKEN` 给 mirror 单独配置 Relay token。双写只作用于事件接入路径，不复制 Release/Artifact 管理请求。

```yaml
SENTRYX_MIRROR_URL: https://sentry.example.com
SENTRYX_MIRROR_RELAY_TOKEN: ""
```

`SENTRYX_MIRROR_URL` 是上游根地址，Relay 会把收到的 `/api/{project}/envelope` 或 `/store` 路径和 query 原样追加到该地址。

建议先把 SentryX 配置为主写、官方 Sentry 为 mirror，对比 Issue 数量与 SDK 响应；稳定后反向切换，最后关闭 mirror。

Compose 还提供 `ui` 服务，默认暴露 `SENTRYX_UI_PORT`（默认 `3000`）。UI 使用 Ant Design，通过 Nginx 同源代理 `/api/0`，不会把 SDK DSN 或私有上报协议改成 SentryX 专用格式。

## 控制面 API

管理 API 使用 `Authorization: Bearer <token>`，兼容旧部署的 `X-SentryX-Management-Token`。本地开发在未配置 token 时保持开放；生产应设置 `SENTRYX_API_TOKENS=token:user-id`。

已实现的最小 Sentry API 子集：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/0/organizations/` | 组织列表 |
| GET | `/api/0/organizations/{org}` | 按 id 或 slug 获取组织 |
| GET/POST | `/api/0/organizations/{org}/teams/` | 团队列表/创建 |
| GET/POST | `/api/0/organizations/{org}/projects/` | 项目列表/创建 |
| GET | `/api/0/organizations/{org}/members/` | 成员列表 |
| GET | `/api/0/projects/{org}/{project}` | 项目详情与 keys |
| GET | `/api/0/projects/{org}/{project}/teams/` | 项目团队 |
| POST | `/api/0/projects/{org}/{project}/teams/{team}` | 关联团队 |
| GET | `/api/0/users/me/` | 当前用户 |

首次收到未知 project ID 的 Envelope 时，Server 会在默认 Organization 下幂等创建项目和 public key；因此可以先切 DSN，再通过控制面补齐组织、团队和项目关系。

## PostgreSQL

`migrations/005_control_plane.sql` 创建控制面表，并预置 `default` 组织。迁移不修改现有事件表；已有项目在首次 ingest 时自动映射到控制面。未配置 `SENTRYX_PROJECT_KEYS` 时保持历史兼容模式，仍接受原来的 `sentry_key`；配置后才启用项目级 key 白名单。

## 兼容边界

这不是完整 Sentry SaaS 管理面：复杂 RBAC、邀请/SCIM、审计日志、Discover/Stats、告警规则以及 Sentry 私有内部 API 仍未承诺兼容。事件接入、Envelope item、Store、Release、Source Map 和基本组织资源是渐进迁移的稳定边界。
