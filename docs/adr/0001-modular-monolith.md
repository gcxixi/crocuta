# ADR-0001：模块化单体与 PostgreSQL 持久队列

状态：Accepted

## 背景

SentryX 要重写 Relay 与 Server，但第一阶段只处理 Web Error。过早引入多个微服务、Kafka、Redis 和分析数据库会显著增加开发与运维成本；只使用进程内 channel 又无法保证“成功响应后不丢事件”。

## 决策

- Relay 作为独立、无状态 Go 二进制。
- Server 作为模块化单体 Go 二进制，以 `api`、`worker`、`all` 角色部署。
- 初期使用 PostgreSQL `ingest_jobs` + `FOR UPDATE SKIP LOCKED` 实现持久租约队列，受限 Envelope payload 存 `BYTEA`。
- 领域层依赖 `Queue` 和 `BlobStore` 接口，不依赖 PostgreSQL 细节。
- 完成任务后快速清除 payload，使用积压年龄、WAL、表膨胀和查询延迟作为拆分依据。

## 结果

优点是依赖少、接受边界清晰、事务和幂等实现简单，API/Worker 仍可独立扩容。代价是高吞吐下 PostgreSQL 会承担额外 WAL 与存储压力，需要严格 payload 上限、短 TTL、清理和容量监控。

当可量化指标显示 PostgreSQL 队列成为瓶颈时，保持 lease/ack/nack 语义迁移到 JetStream/Kafka；这是一项实现替换，不改变 Sentry 接入协议和处理器。
