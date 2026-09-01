import * as Sentry from "@sentry/node";

const dsn = process.env.SENTRYX_DSN || "http://public@127.0.0.1:8081/1";

Sentry.init({
  dsn,
  release: "sentryx-source-e2e@1.0.0",
  environment: "e2e",
  tracesSampleRate: 0,
});

const baseURL = process.env.SENTRYX_BASE_URL || process.env.SENTRYX_SERVER_URL || "http://127.0.0.1:33000";
if (baseURL) {
  const form = new FormData();
  form.append("file", new Blob([JSON.stringify({
    version: 3,
    file: "app.min.js",
    sources: ["src/app.ts"],
    names: ["checkout"],
    mappings: "AAAAA",
  })], { type: "application/json" }), "app.min.js");
  form.append("name", "app.min.js");
  const headers = {};
  if (process.env.SENTRYX_ARTIFACT_TOKEN) {
    headers["X-SentryX-Management-Token"] = process.env.SENTRYX_ARTIFACT_TOKEN;
  }
  const upload = await fetch(`${baseURL}/api/0/projects/1/releases/sentryx-source-e2e%401.0.0/files/`, {
    method: "POST",
    body: form,
    headers,
  });
  if (!upload.ok) throw new Error(`source map upload failed: ${upload.status}`);
}

Sentry.captureEvent({
  platform: "javascript",
  release: "sentryx-source-e2e@1.0.0",
  message: "source map lookup failed",
  exception: {
    values: [{
      type: "Error",
      value: "source map lookup failed",
      stacktrace: {
        frames: [{
          filename: "app.min.js",
          function: "minified",
          lineno: 1,
          colno: 0,
          in_app: true,
        }],
      },
    }],
  },
});

if (!(await Sentry.flush(5000))) {
  throw new Error("Sentry SDK did not flush before timeout");
}
