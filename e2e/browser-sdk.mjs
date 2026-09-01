import { JSDOM } from "jsdom";

const dsn = process.env.SENTRYX_DSN || "http://public@127.0.0.1:8081/1";

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://sentryx-e2e.test/checkout",
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
Object.defineProperty(globalThis, "location", { value: dom.window.location, configurable: true });
globalThis.fetch = fetch;
globalThis.window.fetch = fetch;

const Sentry = await import("@sentry/browser");
Sentry.init({
  dsn,
  release: "sentryx-browser-e2e@1.0.0",
  environment: "e2e",
  defaultIntegrations: false,
  sendDefaultPii: false,
});

Sentry.captureException(new Error("browser checkout failed for order 123456"));
Sentry.captureException(new Error("browser checkout failed for order 987654"));

if (!(await Sentry.flush(5000))) {
  throw new Error("Sentry browser SDK did not flush before timeout");
}
