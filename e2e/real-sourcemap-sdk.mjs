import * as Sentry from "@sentry/node";
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import esbuild from "../ui/node_modules/esbuild/lib/main.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const dsn = process.env.SENTRYX_DSN || "http://public@127.0.0.1:8081/1";

const release = process.env.SENTRYX_RELEASE || "sentryx-real-sourcemap-e2e@1.0.0";
const project = process.env.SENTRYX_PROJECT || "1";

// 1. Generate real source file, minified bundle and genuine V3 sourcemap using esbuild
const bundlePath = path.join(__dirname, "checkout.min.js");
const mapPath = path.join(__dirname, "checkout.min.js.map");

const buildResult = esbuild.buildSync({
  stdin: {
    contents: `
export function checkoutFlow(orderId, amount) {
  validatePayment(orderId, amount);
}

function validatePayment(orderId, amount) {
  if (amount > 100) {
    throw new Error("Payment declined for order " + orderId + ": insufficient funds (" + amount + ")");
  }
}
`,
    sourcefile: "src/checkout-flow.ts",
    loader: "ts",
  },
  outfile: bundlePath,
  minify: true,
  sourcemap: "external",
  format: "esm",
  write: true,
});

const sourceMapContent = fs.readFileSync(mapPath, "utf-8");

// 2. Upload the genuine Source Map to SentryX Server Release Artifacts API
const baseURL = process.env.SENTRYX_BASE_URL || process.env.SENTRYX_SERVER_URL || "http://127.0.0.1:33000";
if (baseURL) {
  const form = new FormData();
  form.append("file", new Blob([sourceMapContent], { type: "application/json" }), "checkout.min.js");
  form.append("name", "checkout.min.js");
  const headers = {};
  if (process.env.SENTRYX_ARTIFACT_TOKEN) {
    headers["X-SentryX-Management-Token"] = process.env.SENTRYX_ARTIFACT_TOKEN;
  }
  const uploadResp = await fetch(`${baseURL}/api/0/projects/${project}/releases/${encodeURIComponent(release)}/files/`, {
    method: "POST",
    body: form,
    headers,
  });
  if (!uploadResp.ok) {
    const errorText = await uploadResp.text();
    throw new Error(`Source map upload failed (${uploadResp.status}): ${errorText}`);
  }
}

// 3. Initialize official @sentry/node SDK
Sentry.init({
  dsn,
  release,
  environment: "e2e-real",
  tracesSampleRate: 0,
});

// 4. Record real user breadcrumbs (测试 breadcrumbs)
Sentry.addBreadcrumb({
  category: "auth",
  message: "User logged in: customer@example.com",
  level: "info",
  timestamp: Date.now() / 1000 - 10,
});

Sentry.addBreadcrumb({
  category: "ui.click",
  message: "User clicked submit payment button",
  level: "info",
  data: {
    cart_id: "cart-998877",
    currency: "USD",
    total_amount: 199.99,
  },
  timestamp: Date.now() / 1000 - 5,
});

Sentry.addBreadcrumb({
  category: "http",
  message: "POST https://api.payment-gateway.internal/v1/charges",
  level: "warning",
  data: {
    status_code: 402,
    error_code: "insufficient_funds",
  },
  timestamp: Date.now() / 1000 - 1,
});

// 5. Execute minified bundle dynamically to throw a genuine Error at runtime
const { checkoutFlow } = await import("./checkout.min.js");

try {
  checkoutFlow("ORD-2026-X", 199.99);
} catch (err) {
  // Capture real thrown JavaScript error through Sentry SDK
  Sentry.captureException(err);
}

// 6. Flush SDK event to Relay
if (!(await Sentry.flush(5000))) {
  throw new Error("Sentry SDK flush timed out");
}

// Clean up temporary generated files
try {
  if (fs.existsSync(bundlePath)) fs.unlinkSync(bundlePath);
  if (fs.existsSync(mapPath)) fs.unlinkSync(mapPath);
} catch {}
