# 实施路线与里程碑

路线按可验证退出标准推进，不以“代码写完”作为完成。每个阶段都保持可部署和可回滚。

## Phase 0：工程骨架与协议基线

交付：

- 建立 Go workspace、两个 `cmd`、内部包边界、配置、日志、metrics、migration 和 Compose。
- 定义 Envelope/Event DTO、Canonical Signal、ProcessorRegistry、Queue、BlobStore 接口。
- 建立 PostgreSQL schema 第一版和 expand/contract migration 流程。
- 建立 Browser、Node、React、Vue、Angular 最小样例生成器与匿名化 golden fixtures。
- 建立 parser fuzz、lint、unit/integration/contract CI。

退出标准：空服务可启动；Relay -> Server -> PostgreSQL 的探针链路贯通；兼容 fixture 可重复生成；架构边界由依赖检查保护。

## Phase 1：可持久接入的垂直切片

交付：

- Relay Envelope/Store、DSN key、CORS、限额、gzip、internal forwarding。
- Server 原始 Envelope 持久任务、Worker lease/ack/nack/dead-letter。
- JavaScript Error 基础 decode/validate/normalize/scrub。
- Event 幂等写入与最小 Event 查询 API。
- 接入、积压、错误原因 metrics 和 trace。

退出标准：所有目标 SDK 的普通 Error 可只改 DSN 入库；返回成功后 kill API/Worker 不丢事件；重复 event ID 不重复入库；恶意 parser corpus 不崩溃、不无界占用内存。

## Phase 2：Source Map 与 Issue 聚合

交付：

- Release 与 legacy Source Map 上传 API、BlobStore、artifact 匹配。
- JavaScript/TypeScript symbolication，保留 minified/original frame 与解释信息。
- grouping v1、custom fingerprint、component tree、并发 Issue upsert。
- Issue 列表/详情/状态、Issue 下 Event 分页。
- debug ID/artifact bundle 支持作为本阶段 stretch goal；若未完成则进入紧随其后的小版本。

退出标准：兼容矩阵中的 Source Map 用例通过；相同逻辑错误跨常规构建稳定聚合；不同根因 fixture 不误合并；并发和重复投递不造成 Issue 计数膨胀。

## Phase 3：生产加固

交付：

- 项目配置缓存、分层限流、backpressure、管理 token scope、审计日志。
- 数据保留、payload 清理、数据库分区/索引验证、备份恢复演练。
- 负载、积压恢复、故障注入、安全测试和运行手册。
- Dashboard、SLO、告警以及 dead job/reprocess 管理工具。

退出标准：达到技术方案容量/SLO 基线；数据库或 BlobStore 故障时行为符合接受边界；恢复演练、PII 检查和安全评审通过；值班人员可仅凭 runbook 定位常见故障。

## Phase 4：扩展能力（按需求排序）

候选流：

1. 新语言 Error Processor（优先依据实际 SDK 流量）。
2. Performance transaction/span、动态采样与分析存储。
3. Replay metadata/recording、Blob TTL 和隐私清洗。
4. 原生 crash 与独立 Symbolicator。
5. 队列迁移到 JetStream/Kafka、分析查询迁移到 ClickHouse。

进入任何候选流前必须有容量/用户需求证据，并先扩展兼容矩阵、数据保留和安全模型。

## 建议 Issue 拆分

- Epic A：协议与 Relay
- Epic B：持久接入队列
- Epic C：Canonical JavaScript Error
- Epic D：Source Map 与 Release
- Epic E：Grouping 与 Issue
- Epic F：查询/管理 API
- Epic G：安全、可靠性与可观测性
- Epic H：SDK compatibility lab

每个实现 Issue 必须标注影响的兼容矩阵行、数据 migration、指标、失败语义和测试类型。
