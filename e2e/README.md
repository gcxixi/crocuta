# SDK E2E 测试

`sdk_e2e_test.go` 启动两个 `httptest` 服务：Server 内存存储和 Relay 转发层。测试通过 Node 子进程执行官方 SDK，避免手写请求掩盖协议兼容问题。

- `node-sdk.mjs`：`@sentry/node` 捕获两个动态订单号错误。
- `browser-sdk.mjs`：在 jsdom 中运行 `@sentry/browser`，模拟浏览器运行时并发送两个错误。
- `framework-sdk.mjs`：直接加载官方 `@sentry/react`、`@sentry/vue`、`@sentry/angular`，验证框架 SDK 共享的 JavaScript Error 上报契约。
- `sourcemap-sdk.mjs`：通过 `@sentry/node` 发送带压缩堆栈的事件，验证 Source Map 符号化。

设置 `SENTRYX_ARTIFACT_TOKEN` 时，SourceMap 脚本会用管理 token 上传 artifact，可用于验证生产鉴权配置。

运行：

```bash
npm install
npm run test:e2e
```

E2E 断言包括 Relay 转发成功、Server 接收事件、Node/Browser 事件聚合到同一个 Issue、React/Vue/Angular 各自产生 JavaScript Event、Issue `count == 2`，以及上传 Source Map 后恢复 `src/app.ts`/`checkout` 原始 frame。默认测试使用内存实现以保持快速回归；PostgreSQL 后端通过 Docker Compose 运行同一组官方 SDK 脚本验证。
