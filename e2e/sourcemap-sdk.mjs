import * as Sentry from "@sentry/node";

const dsn = process.env.SENTRYX_DSN;
if (!dsn) throw new Error("SENTRYX_DSN is required");

Sentry.init({
  dsn,
  release: "sentryx-source-e2e@1.0.0",
  environment: "e2e",
  tracesSampleRate: 0,
});

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
