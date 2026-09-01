# NUC 真实 SDK E2E Runbook

本文在 NUC 上验证完整链路：官方 JavaScript SDK → Relay → Server API →
PostgreSQL ingest job → Server Worker → Event/Issue/Artifact。

## 启动

NUC 已有 PostgreSQL 服务时，使用独立端口避免冲突：

```bash
cd /home/joker/sentryx-e2e
SENTRYX_POSTGRES_PORT=15432 docker compose up -d --build
docker compose ps
curl -fsS http://127.0.0.1:8081/health/live
```

## 运行官方 SDK

NUC 不需要安装 Node；测试依赖在一次性 Node 容器中安装：

```bash
docker run --rm --network host \
  -v /home/joker/sentryx-e2e:/src:ro node:24-alpine sh -lc '
    rm -rf /tmp/sentryx-sdk-e2e && mkdir -p /tmp/sentryx-sdk-e2e
    cp /src/package.json /src/package-lock.json /tmp/sentryx-sdk-e2e/
    cp -R /src/e2e /tmp/sentryx-sdk-e2e/
    cd /tmp/sentryx-sdk-e2e && npm ci --silent
    SENTRYX_DSN=http://public@127.0.0.1:8081/1 node e2e/node-sdk.mjs
    SENTRYX_DSN=http://public@127.0.0.1:8081/1 node e2e/browser-sdk.mjs
    SENTRYX_DSN=http://public@127.0.0.1:8081/1 node e2e/framework-sdk.mjs
    SENTRYX_DSN=http://public@127.0.0.1:8081/1 \
      SENTRYX_BASE_URL=http://127.0.0.1:8081 node e2e/sourcemap-sdk.mjs
  '
```

脚本覆盖 `@sentry/node`、`@sentry/browser`、`@sentry/react`、`@sentry/vue`、
`@sentry/angular` 和 Source Map 上传/符号化。

## 核对持久化与恢复

```bash
docker compose exec -T postgres psql -U sentryx -d sentryx -c \
  'select state, count(*) from sentryx_ingest_jobs group by state;
   select count(*) as events from sentryx_events;
   select count(*) as issues from sentryx_issues;
   select count(*) as artifacts from sentryx_artifacts;'

docker compose restart server
docker compose exec -T postgres psql -U sentryx -d sentryx -c \
  'select count(*) as events from sentryx_events;
   select count(*) as issues from sentryx_issues;
   select count(*) as artifacts from sentryx_artifacts;'
```

重启前后计数应保持不变；Source Map 事件的 `canonical_json` 应包含
`symbolication_status = symbolicated` 以及 `src/app.ts` 原始 frame。

## 停止

```bash
docker compose down
```

不要在常规回归中使用 `down -v`，以免删除持久化验证数据。
