import * as Sentry from "@sentry/node";

const dsn = process.env.SENTRYX_DSN || "http://public@127.0.0.1:8081/1";

Sentry.init({
  dsn,
  release: "sentryx-e2e@1.0.0",
  environment: "e2e",
  tracesSampleRate: 0,
  sendDefaultPii: false,
});

Sentry.captureException(new Error("checkout failed for order 123456"));
Sentry.captureException(new Error("checkout failed for order 987654"));

if (!(await Sentry.flush(5000))) {
  throw new Error("Sentry SDK did not flush before timeout");
}
