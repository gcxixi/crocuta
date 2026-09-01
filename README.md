# SentryX

SentryX 是一个以 Go 为统一技术栈、面向 Sentry SDK 协议兼容的错误采集与 Issue 聚合系统。

第一阶段只处理 JavaScript/TypeScript Web 生态的错误事件，包括 Node.js、React、Vue、Angular 等 SDK；Relay 与 Server 均使用 Go 实现。系统从第一天保留 Signal、Processor、Queue 与 BlobStore 扩展点，以便后续增加其他语言、Performance、Replay、Logs 等能力。

## 第一阶段边界

- 兼容 Sentry Envelope 与旧版 Store 上报入口。
- 支持 DSN 公钥鉴权、限流、大小限制、解压与安全校验。
- 解析 `event`/`error` item，兼容 JavaScript、TypeScript、Node.js 及主流前端框架 SDK。
- 支持 Source Map 上传、匹配与符号化的最小闭环。
- 支持事件去重、稳定分组、Issue 聚合与基础查询 API。
- 暂不实现告警、通知、完整 Sentry 管理 API、Performance、Replay、Session 与原生崩溃解析。

## 架构摘要

```text
Sentry SDK
    |
    v
sentryx-relay (Go)
    |  流式校验、鉴权、限流、转发
    v
sentryx-server ingest API (Go)
    |  原始 Envelope + 持久任务
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
- [分阶段实施路线](docs/roadmap.md)
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

当前仓库完成了产品边界、兼容策略、总体架构、数据模型、分组算法、可靠性与演进路线设计；下一步按 `docs/roadmap.md` 的 Phase 0 建立 Go 工程骨架和 SDK 契约测试。
