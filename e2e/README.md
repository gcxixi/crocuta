# SDK E2E 测试

`sdk_e2e_test.go` 启动两个 `httptest` 服务：Server 内存存储和 Relay 转发层。测试通过 Node 子进程执行官方 SDK，避免手写请求掩盖协议兼容问题。

- `node-sdk.mjs`：`@sentry/node` 捕获两个动态订单号错误。
- `browser-sdk.mjs`：在 jsdom 中运行 `@sentry/browser`，模拟浏览器运行时并发送两个错误。

运行：

```bash
npm install
npm run test:e2e
```

E2E 断言包括 Relay 转发成功、Server 接收两个事件、事件按规范化内容聚合到同一个 Issue，以及 Issue `count == 2`。当前测试故意使用内存实现；接入 PostgreSQL 后应保留同一套 SDK 测试并将服务替换为真实进程/Compose。
