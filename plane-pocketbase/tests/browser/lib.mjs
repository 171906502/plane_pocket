// Shared helpers for the Plane + PocketBase browser workflow harness.
//
// Uses playwright-core driving the locally installed Microsoft Edge (Chromium),
// so no browser download is required. A persistent user-data dir keeps the
// login session (cookies) alive across separate script invocations.
import { chromium } from "playwright-core";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Derived from this file's own location (…/tests/browser/lib.mjs) rather than
// hardcoded, so a clone in another directory works. Was
// "C:\\Users\\cao\\Desktop\\asl-work\\plane_pocket\\plane-pocketbase\\tests".
const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.dirname(HERE);

export const EDGE =
  process.env.PLANE_EDGE ||
  "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
export const PROFILE = path.join(ROOT, "browser", "profile");
export const SHOTS = path.join(ROOT, "evidence", "screenshots");
export const NETLOG = path.join(ROOT, "evidence", "network.log");

export const WEB = "http://localhost:3000";
export const SPACE = "http://localhost:3002";
export const ADMIN = "http://localhost:3001";

export async function launch({ headless = true } = {}) {
  fs.mkdirSync(SHOTS, { recursive: true });
  fs.mkdirSync(PROFILE, { recursive: true });

  const ctx = await chromium.launchPersistentContext(PROFILE, {
    executablePath: EDGE,
    headless,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    args: [
      "--disable-blink-features=AutomationControlled",
      "--no-first-run",
      "--no-default-browser-check",
      // The machine may sit behind a system proxy; localhost must bypass it or
      // navigation to the dev servers hangs until the timeout.
      "--no-proxy-server",
      "--proxy-bypass-list=*",
    ],
  });

  const page = ctx.pages()[0] ?? (await ctx.newPage());
  page.setDefaultNavigationTimeout(90_000);
  page.setDefaultTimeout(30_000);

  // Record every request that hits our own stack so the test document can cite
  // the real HTTP conversation the browser produced.
  const net = [];
  page.on("response", (res) => {
    const req = res.request();
    const url = req.url();
    if (!/:(3000|3001|3002|8000)\//.test(url)) return;
    net.push({
      method: req.method(),
      // Normalise every dev-server origin away so the log reads as API paths.
      // 3002 matters: the Space app is configured with an absolute
      // VITE_API_BASE_URL, but its own /api/* calls still come from 3002.
      url: url.replace(/^http:\/\/localhost:(3000|3001|3002|8000)/, ""),
      status: res.status(),
      resourceType: req.resourceType(),
    });
  });

  return { ctx, page, net };
}

export function mkLogger(name) {
  const lines = [];
  const api = {
    log: (...a) => {
      const s = a.join(" ");
      lines.push(s);
      console.log(s);
    },
    dump: () => lines.join("\n"),
  };
  return api;
}

export async function shot(page, name, note) {
  const file = path.join(SHOTS, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log(`  [shot] ${name}.png${note ? "  (" + note + ")" : ""}`);
  return file;
}

/** Wait until the page stops mutating for `quiet` ms, or `max` elapses. */
export async function settle(page, quiet = 700, max = 8000) {
  const start = Date.now();
  let last = Date.now();
  let strikes = 0;
  while (Date.now() - start < max) {
    await page.waitForTimeout(150);
    // `page.evaluate` has no default timeout, and the Plane page editor keeps the
    // renderer busy enough that it can block indefinitely. Race it so a wedged
    // renderer degrades into "assume settled" instead of hanging the whole run.
    const changed = await Promise.race([
      page
        .evaluate(() => {
          const h = document.documentElement.innerHTML.length;
          const same = window.__lastLen === h;
          window.__lastLen = h;
          return !same;
        })
        .catch(() => false),
      new Promise((r) => setTimeout(() => r("timeout"), 5000)),
    ]);
    if (changed === "timeout") {
      if (++strikes >= 2) return;
      continue;
    }
    strikes = 0;
    if (changed) last = Date.now();
    else if (Date.now() - last >= quiet) return;
  }
}

/**
 * Read `document.body.innerText` with a hard bound.
 *
 * After the rich-text page editor has been used, Plane's renderer can stay busy
 * indefinitely; an unbounded `page.evaluate` then never resolves and wedges the
 * whole chain. Returns "" when the renderer refuses to answer.
 */
export async function safeText(page, ms = 8000) {
  return Promise.race([
    page.evaluate(() => document.body.innerText || "").catch(() => ""),
    new Promise((r) => setTimeout(() => r(""), ms)),
  ]);
}

/**
 * Drop the current renderer and load a blank document.
 *
 * `page.evaluate` blocking forever means the renderer thread is stuck; a
 * navigation tears the process down and gives the next step a fresh one.
 */
export async function resetPage(page) {
  await page.goto("about:blank", { waitUntil: "commit" }).catch(() => {});
  await page.waitForTimeout(500);
}

/** Print a compact list of visible interactive elements so steps can be adapted. */
export async function describe(page) {
  const items = await page.evaluate(() => {
    const out = [];
    const sel = "input,textarea,button,a[href],[role=button],[role=menuitem],select";
    for (const el of document.querySelectorAll(sel)) {
      const r = el.getBoundingClientRect();
      if (r.width < 2 || r.height < 2) continue;
      const cs = getComputedStyle(el);
      if (cs.visibility === "hidden" || cs.display === "none" || cs.opacity === "0") continue;
      const txt = (el.innerText || el.value || el.getAttribute("placeholder") || "").trim().replace(/\s+/g, " ").slice(0, 60);
      out.push({
        tag: el.tagName.toLowerCase(),
        type: el.getAttribute("type") || "",
        name: el.getAttribute("name") || "",
        id: el.id || "",
        ph: el.getAttribute("placeholder") || "",
        txt,
      });
    }
    return out;
  });
  console.log(`  [dom] ${items.length} interactive elements`);
  for (const i of items) {
    console.log(`    <${i.tag}${i.type ? " type=" + i.type : ""}${i.id ? " id=" + i.id : ""}${i.name ? " name=" + i.name : ""}${i.ph ? ' ph="' + i.ph + '"' : ""}> ${i.txt}`);
  }
  return items;
}

export function writeNet(net, label) {
  const header = `\n===== ${label}  (${new Date().toISOString()}) =====\n`;

  const errors = net.filter((n) => n.status >= 400);
  const mutations = net.filter((n) => n.status < 400 && n.method !== "GET" && n.method !== "HEAD");

  // Reads matter for diagnosing contract bugs: whether the frontend sent
  // `group_by=state_id`, which endpoint answered, and with what status. Dedupe
  // on method+url so a polling loop does not drown the log, and keep the query
  // string — the query string *is* the evidence.
  const seen = new Set();
  const reads = [];
  for (const n of net) {
    if (n.method !== "GET" && n.method !== "HEAD") continue;
    if (!n.url.startsWith("/api/")) continue;
    const key = n.status + " " + n.url;
    if (seen.has(key)) continue;
    seen.add(key);
    reads.push(n);
    if (reads.length >= 80) break;
  }

  const fmt = (n) => `${String(n.status).padEnd(4)} ${n.method.padEnd(6)} ${n.url}`;

  const body =
    "-- FAILED REQUESTS --\n" +
    (errors.length ? errors.map(fmt).join("\n") : "(none)") +
    "\n\n-- MUTATIONS --\n" +
    (mutations.length ? mutations.map(fmt).join("\n") : "(none)") +
    "\n\n-- API READS (deduped, query strings kept) --\n" +
    (reads.length ? reads.map(fmt).join("\n") : "(none)") +
    "\n";

  fs.appendFileSync(NETLOG, header + body);
  console.log(header + body);
}

export function randomEmail(prefix) {
  return `${prefix}${Date.now()}@example.com`;
}

/** Wait until the app shell is painted (sidebar present) — SPAs show a spinner first. */
export async function waitForShell(page, timeout = 45000) {
  await page
    .waitForFunction(
      () => {
        const t = document.body.innerText || "";
        return t.includes("Add project") || t.includes("New work item") || t.includes("Your work");
      },
      null,
      { timeout }
    )
    .catch(() => {});
  await waitNoSpinner(page);
}

/** Wait until no spinner is on screen. Degrades quietly if one never disappears. */
export async function waitNoSpinner(page, timeout = 30000) {
  await page
    .waitForFunction(() => !document.querySelector(".animate-spin, [class*='animate-spin']"), null, { timeout })
    .catch(() => {});
}

/** Click the first element whose visible text matches, waiting for it first. */
export async function clickText(page, text, timeout = 20000) {
  const loc = page.getByText(text, { exact: false }).first();
  await loc.waitFor({ state: "visible", timeout });
  await loc.click();
  return true;
}

/** Wait for a dialog/modal to be attached, if one appears. */
export async function waitDialog(page, timeout = 10000) {
  return page
    .waitForSelector("[role=dialog], [data-slot=dialog-content]", { timeout })
    .then(() => true)
    .catch(() => false);
}
