# Feature request implementation

This document maps GitHub Issue #1 to the implementation in SentryX. The wire
contract remains the Sentry Envelope and `/api/0` resource model; the additions
are backwards-compatible query and lifecycle capabilities.

## Ingestion and Relay

`SENTRYX_ITEM_POLICY` is a comma-separated map such as
`transaction:drop,session:drop,replay_event:store,profile:sample:25`.
Supported actions are `store`, `drop`, and `sample:N` (percentage). Unknown
types default to `store`, so future Sentry SDK items are not silently lost.
Relay exposes `/health/ready` and `/metrics`. When
`SENTRYX_MIRROR_SPOOL_DIR` is set, failed mirror deliveries are written as
atomic JSON files and retried every 15 seconds.

## Query and Issue lifecycle

Existing array responses are preserved. Set `limit` (1-100) and follow the
`X-Next-Cursor` response header:

```
GET /api/0/issues/?project=1&start=2026-09-01T00:00:00Z&end=2026-09-02T00:00:00Z&status=unresolved&sort=last_seen&limit=50
GET /api/0/events/?project=1&issue=<issue-id>&environment=production&release=web@1
```

`query` supports the compatible subset `is:unresolved`, `level:error`, and
`title:text`. Update lifecycle state with
`PUT /api/0/issues/<id>/` and body `{"status":"resolved"}` (or
`unresolved`/`ignored`, with optional `resolved_in_release`). A resolved issue
automatically becomes a regression when a later event arrives.

## Analytics and alerts

* `GET /api/0/issues/<id>/series?project=1&resolution=1h`
* `GET /api/0/issues/<id>/tags/<key>/?project=1`
* `GET /api/0/projects/<org>/<project>/stats`
* `GET|POST /api/0/projects/<org>/<project>/alert-rules`
* `PUT|DELETE /api/0/projects/<org>/<project>/alert-rules/<id>`

Alert rules support `new_issue`, `regression`, and event-count conditions,
with a webhook action, threshold, window, cooldown, and enabled flag. The
worker evaluates rules every 30 seconds outside the ingest transaction.

## Retention, security, and reconciliation

Migrations `006_feature_request.sql` and `007_grouping_migration.sql` add issue lifecycle columns, hourly
rollups, alert rules, nullable completed-job payloads, and project retention
configuration, plus persisted grouping-hash mappings and component trees. The worker performs batched cleanup hourly and clears queue
payloads on acknowledgement. `SENTRYX_API_TOKEN_HASHES` accepts
`sha256hex:user-id` entries; plaintext `SENTRYX_API_TOKENS` remains supported
for local compatibility. `cmd/sentryx-reconcile` compares event IDs in the
new and legacy stores and reports both sides' missing IDs. `sentryx-groupctl`
replays JSONL or PostgreSQL events and reports grouping changes between two
algorithm versions.

## Operations

The repository now includes `.github/workflows/ci.yml` for Go formatting,
vet/tests, and Ant Design UI builds. `/health/live`, `/health/ready`, and the
Prometheus text endpoint `/metrics` are available on both server and Relay.
