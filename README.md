# SentryX

SentryX 是一个以 Go 为统一技术栈、面向 Sentry SDK 协议兼容的错误采集与 Issue 聚合系统。

第一阶段只处理 JavaScript/TypeScript Web 生态的错误事件，包括 Node.js、React、Vue、Angular 等 SDK；Relay 与 Server 均使用 Go 实现。系统从第一天保留 Signal、Processor、Queue 与 BlobStore 扩展点，以便后续增加其他语言、Performance、Replay、Logs 等能力。

## 第一阶段边界

- 兼容 Sentry Envelope 与旧版 Store 上报入口。
- 支持 DSN 公钥鉴权、限流、大小限制、解压与安全校验。
- 解析 `event`/`error` item，兼容 JavaScript、TypeScript、Node.js 及主流前端框架 SDK。
- 支持 Source Map 上传、匹配与符号化的最小闭环。
- 支持事件去重、稳定分组、Issue 聚合与基础查询 API。
- 已实现 P0/P1/P2 的协议接收和持久化边界：User/Request/Breadcrumb、gzip/CORS、混合 Envelope、Client Report、Attachment、Release/Artifact 管理，以及 transaction/span、Replay、Session、Profile、Native Crash item 的版本化 StoredSignal。
- 扩展 item 目前提供查询/BlobStore 落盘能力，已提供 Ant Design 错误监控控制台；尚未提供 Performance/Replay 分析 UI、Native Crash 完整 Symbolicator、告警通知和全量 Sentry 管理 API；控制面已支持 Organization/User/Team/Project 的渐进迁移子集。

## 架构摘要

```text
Sentry SDK
    |
    v
sentryx-relay (Go)
    |  流式校验、鉴权、限流、PII 清洗、转发
    v
sentryx-server ingest API (Go)
    |  入队前 PII 清洗 + 持久任务
    v
PostgreSQL queue  ---> sentryx-server worker
                          |  规范化、PII 清洗、Source Map、分组
                          +----> PostgreSQL events/issues
                          +----> BlobStore (Source Map/未来 Replay)
```

Server 是模块化单体，同一二进制支持 `api`、`worker`、`all` 三种运行角色。初期只要求 PostgreSQL；生产 Source Map 使用 S3 兼容 BlobStore，本地开发可使用文件实现。不在第一阶段引入 Kafka、Redis 或 Elasticsearch。

## 文档

- [完整技术方案](docs/technical-design.md)
- [SDK 与协议兼容范围](docs/compatibility.md)
- [UI API 契约](docs/ui-api.md)
- [UI 技术方案（Ant Design）](docs/ui-technical-design.md)
- [分阶段实施路线](docs/roadmap.md)
- [PII 清洗设计](docs/pii.md)
- [控制面与双写迁移](docs/control-plane.md)
- [ADR-0001：模块化单体与 PostgreSQL 持久队列](docs/adr/0001-modular-monolith.md)
- [ADR-0002：版本化 Issue 分组](docs/adr/0002-versioned-grouping.md)

## 建议仓库结构

```text
cmd/
  sentryx-relay/
  sentryx-server/
internal/
  contracts/sentry/       # Envelope、Event、协议错误
  relay/                  # 边缘入口
  ingest/                 # Server 接收与持久化
  processing/             # Processor 注册表与流水线
  processing/javascript/  # JS/TS 规范化、Source Map
  grouping/               # 版本化分组算法
  issues/                 # Issue 聚合领域
  storage/postgres/
  blob/
  queue/
  api/
migrations/
deploy/
docs/
testdata/sentry/
```

## 当前状态

已完成首个可运行垂直切片：Go Relay、Go Server、Envelope/Store 接入、JavaScript Error 规范化、基础分组与 Issue 查询；Node.js、Browser、React、Vue、Angular 官方 Sentry SDK 的端到端测试均已通过。Source Map/Release 已具备内存 MVP，并由官方 SDK E2E 验证上传、匹配和符号化；PostgreSQL 后端已支持 ingest job、Event/Issue 和 Artifact 持久化，以及 `api/worker/all` 角色。ArtifactStore 现在支持数据库兼容模式、文件 BlobStore 和 S3 兼容 BlobStore；生产鉴权、限流和生命周期策略仍按 `docs/roadmap.md` 推进。

## 本地验证

需要 Go、Node.js 和 npm：

```bash
npm install
npm run test:e2e
```

测试会在进程内启动 Relay 与 Server，并让官方 `@sentry/node`、`@sentry/browser` SDK 将两个不同动态订单号的错误发送到 Relay，断言最终只有一个 Issue 且事件计数为 2。

PostgreSQL 运行模式：先执行 `migrations/001_init.sql`，再设置 `SENTRYX_DATABASE_URL`。`sentryx-server -role=all` 同时提供 API 和 Worker；生产环境可拆为 `-role=api` 与 `-role=worker` 两组实例。

Source Map 存储通过 `SENTRYX_BLOB_BACKEND` 选择：`database`（默认，兼容旧 schema）、`file`（设置 `SENTRYX_BLOB_DIR`）或 `s3`。S3 模式需要 `SENTRYX_S3_ENDPOINT`、`SENTRYX_S3_ACCESS_KEY`、`SENTRYX_S3_SECRET_KEY`、`SENTRYX_S3_BUCKET`，可选 `SENTRYX_S3_REGION`、`SENTRYX_S3_PREFIX` 和 `SENTRYX_S3_SECURE`。切换已有 PostgreSQL 数据库前先执行 `migrations/002_blobstore.sql`；旧 Worker 可在滚动升级期间继续读取 BYTEA。

生产接入可设置 `SENTRYX_PROJECT_KEYS=project:key,...` 开启项目公钥白名单，设置 `SENTRYX_ARTIFACT_TOKEN` 保护 Source Map、Release 和 Artifact 管理接口，并用 `SENTRYX_RATE_LIMIT_PER_MINUTE` 开启每个 project/key/client 的进程内限流；未设置时保持开发兼容模式。新增扩展 item 和附件/Client Report 数据表由 `migrations/003_extended_items.sql` 创建，Debug ID 索引由 `migrations/004_artifact_debug_id.sql` 创建。Issue 生命周期、小时级统计、告警规则、保留期和队列 payload 清理由 `migrations/006_feature_request.sql` 创建，分组 hash 映射由 `migrations/007_grouping_migration.sql` 创建。

Relay 可通过 `SENTRYX_ITEM_POLICY=transaction:drop,session:drop,replay_event:store,profile:sample:25` 在边缘丢弃或采样非 error item；双写失败可设置 `SENTRYX_MIRROR_SPOOL_DIR` 开启磁盘重放。管理 token 推荐使用 `SENTRYX_API_TOKEN_HASHES=sha256hex:user-id`，明文 `SENTRYX_API_TOKENS` 仍用于兼容旧部署。查询分页、Issue 状态、分析和告警 API 详见 [`docs/FEATURE_REQUEST.md`](docs/FEATURE_REQUEST.md)。

分组算法升级前可使用 `sentryx-groupctl replay --source=pg --version=v2 --compare=v1 --project=<id>` 进行离线回放对比。

NUC 真实 SDK E2E 的启动、执行、数据库核对和重启恢复步骤见 [`docs/e2e-nuc.md`](docs/e2e-nuc.md)。
