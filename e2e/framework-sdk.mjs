// Smoke-test the official framework SDK entry points without requiring a
// framework build. Each package must still initialize and emit a normal
// JavaScript error through the same transport contract.
const dsn = process.env.SENTRYX_DSN || "http://public@127.0.0.1:8081/1";

const common = {
  dsn,
  environment: "e2e",
  tracesSampleRate: 0,
  defaultIntegrations: false,
  sendDefaultPii: false,
};

const sdkCases = [
  ["react", "@sentry/react", "sentryx-react-e2e@1.0.0"],
  ["vue", "@sentry/vue", "sentryx-vue-e2e@1.0.0"],
  ["angular", "@sentry/angular", "sentryx-angular-e2e@1.0.0"],
];

for (const [name, packageName, release] of sdkCases) {
  // Angular packages are partially compiled and need the JIT compiler when
  // exercised directly from Node instead of an Angular CLI bundle.
  if (name === "angular") await import("@angular/compiler");
  const Sentry = await import(packageName);
  Sentry.init({ ...common, release });
  Sentry.captureException(new Error(`${name} checkout failed for order 987654`));
  if (!(await Sentry.flush(5000))) {
    throw new Error(`${packageName} did not flush before timeout`);
  }
}
