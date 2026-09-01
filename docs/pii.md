# SentryX PII 清洗设计

SentryX 参考 Sentry Relay 与 GlitchTip 的入队前清洗思路，采用三层防线：SDK 可在 `beforeSend` 清洗；Relay 在边缘清洗；Server 在入队前和 Worker 入库前再次清洗。这样即使绕过 Relay 或使用旧版本 Relay，数据库和持久队列仍不会依赖单一清洗点。

## 数据流

```text
SDK
  -> Relay（可选，默认开启 PII）
  -> Server Ingest（入队前清洗）
  -> ingest_jobs（只保存清洗后的 Envelope）
  -> Worker（再次清洗）
  -> Event / Signal / Attachment / Issue
```

Relay 仍然是无状态、可横向扩展的边缘组件；Server 是最终权威。没有 Relay 的单机部署也会在 Server Ingest 中执行相同策略。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `SENTRYX_PII_ENABLED` | `true` | 总开关；关闭后保留原始 Envelope，只有调试环境建议关闭 |
| `SENTRYX_PII_SCRUB_DEFAULTS` | `true` | 启用默认敏感 key、Bearer/token、信用卡号规则 |
| `SENTRYX_PII_SCRUB_IP_ADDRESSES` | `true` | 清洗 `user.ip_address` |
| `SENTRYX_PII_SENSITIVE_FIELDS` | 空 | 逗号分隔的字段名或路径，例如 `customer_id,extra.internal_note` |
| `SENTRYX_PII_SAFE_FIELDS` | 空 | 默认规则排除字段；应谨慎使用 |
| `SENTRYX_PII_RULES` | 空 | JSON 规则数组，或 `{"rules":[...]}` |

Relay 和 Server 应使用相同配置。生产环境不要只在 Relay 设置规则；Server 必须保留兜底清洗。

## 规则格式

```json
[
  {
    "id": "authorization",
    "selector": "request.headers.authorization",
    "type": "sensitive",
    "action": "mask"
  },
  {
    "id": "customer-hash",
    "selector": "extra.customer_id",
    "type": "anything",
    "action": "hash"
  },
  {
    "id": "email",
    "selector": "$message",
    "type": "email",
    "pattern": "[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}",
    "action": "replace",
    "replacement": "[Email]"
  }
]
```

支持的 selector 第一阶段包括 `$message`、字段路径（如 `extra.foo`）、请求路径和 `.*` 后缀通配；支持 `mask`、`remove`、`hash`、`replace`。非法正则会被忽略，不应导致整个 Ingest 不可用。

## 覆盖范围

- `event` / `error` / `log`：递归 JSON 清洗，并写入 `_meta` 清洗原因。
- `client_report`：JSON 字段经过同一规则，默认只保留协议统计字段。
- `attachment`：JSON 和文本类型清洗；二进制 Attachment 只保存元数据和原始字节，后续需要按类型增加专用 scrubber。
- transaction、span、replay、session、profile 等 JSON Signal：递归清洗后以 `StoredSignal` 保存。
- minidump、native crash 等二进制 Signal：不猜测二进制结构，保留专用处理器扩展点。
- 混合 Envelope：逐 Item 清洗和重写长度，一个不支持的 Item 不影响其他 Item。

## `_meta` 与可观测性

Event JSON 会写入类似以下结构：

```json
{
  "_meta": {
    "request.headers.authorization": {"reason": "@password"},
    "user.ip_address": {"reason": "@ip"}
  }
}
```

后续 UI 可据此解释字段被清洗的原因。服务端应增加清洗计数、规则命中数、Attachment 类型和清洗失败指标；清洗失败默认拒绝当前 Envelope，而不是将原文继续入队。

## 与 GlitchTip 的关系

GlitchTip 的近期实现把可选 server-side PII scrubber 放在 Ingest、入队和持久化之前，作为 SDK 未配置清洗时的最后防线。SentryX 采用相同的时机，但把实现统一为 Go，并额外在 Relay 做边缘清洗和在 Worker 做防御性重复清洗。只参考公开 API 和架构，不复制受许可证约束的实现代码。
