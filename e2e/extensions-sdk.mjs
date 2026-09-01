const base = process.env.SENTRYX_BASE_URL || "http://127.0.0.1:8081";
const queryBase = process.env.SENTRYX_QUERY_URL || base;
const project = process.env.SENTRYX_PROJECT || "1";
const key = process.env.SENTRYX_KEY || "public";
const eventID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";

const event = JSON.stringify({
  event_id: eventID,
  platform: "javascript",
  message: "extension e2e",
  user: { id: "u-1", ip_address: "203.0.113.7" },
  request: { url: "https://example.test/checkout?token=secret", method: "POST", headers: { Authorization: "Bearer hidden" } },
  breadcrumbs: { values: [{ category: "ui.click", message: "pay" }] },
  contexts: { browser: { name: "Chromium" } },
});
const report = JSON.stringify({ timestamp: new Date().toISOString(), discarded_events: [{ reason: "sample_rate", category: "error", quantity: 3 }] });
const transaction = JSON.stringify({ event_id: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", transaction: "checkout", spans: [] });
const attachment = Buffer.from("extension attachment");
const replay = Buffer.from("binary replay payload");
const minidump = Buffer.from("binary minidump payload");
const part = (header, body) => Buffer.concat([Buffer.from(header + "\n"), Buffer.isBuffer(body) ? body : Buffer.from(body), Buffer.from("\n")]);
const body = Buffer.concat([
  Buffer.from("{}\n"),
  part(JSON.stringify({ type: "event", length: Buffer.byteLength(event) }), event),
  part(JSON.stringify({ type: "client_report", length: Buffer.byteLength(report) }), report),
  part(JSON.stringify({ type: "attachment", length: attachment.length, event_id: eventID, filename: "extension.log", content_type: "text/plain" }), attachment),
  part(JSON.stringify({ type: "transaction", length: Buffer.byteLength(transaction), event_id: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" }), transaction),
  part(JSON.stringify({ type: "replay_recording", length: replay.length, event_id: eventID, content_type: "application/octet-stream" }), replay),
  part(JSON.stringify({ type: "minidump", length: minidump.length, event_id: eventID, content_type: "application/octet-stream" }), minidump),
]);

const response = await fetch(`${base}/api/${project}/envelope?sentry_key=${key}`, { method: "POST", body, headers: { "content-type": "application/x-sentry-envelope" } });
if (!response.ok) throw new Error(`ingest ${response.status}: ${await response.text()}`);
console.log("ingest", await response.text());
await new Promise((resolve) => setTimeout(resolve, 1500));

for (const endpoint of [
  `/api/0/issues?project=${project}`,
  `/api/0/client-reports?project=${project}`,
  `/api/0/attachments?project=${project}`,
  `/api/0/signals?project=${project}&kind=replay_recording`,
  `/api/0/signals?project=${project}&kind=minidump`,
]) {
  if (!process.env.SENTRYX_QUERY_URL) continue;
  const result = await fetch(queryBase + endpoint);
  if (!result.ok) throw new Error(`${endpoint} ${result.status}`);
  const text = await result.text();
  console.log(endpoint, text);
  if (endpoint.includes("attachments")) {
    const list = JSON.parse(text);
    if (list.length !== 1) throw new Error(`attachment count ${list.length}`);
    const download = await fetch(`${queryBase}/api/0/attachments/${list[0].id}?project=${project}`);
    const bytes = Buffer.from(await download.arrayBuffer());
    if (bytes.toString() !== "extension attachment") throw new Error("attachment body mismatch");
    console.log("attachment download", bytes.length);
  }
}
