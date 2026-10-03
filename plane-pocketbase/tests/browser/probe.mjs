// Generic page probe: log every /api/ response (status + body head), plus the
// browser console and any uncaught page error, for a given URL.
//
//   node probe.mjs /hrms/projects/
//
// The console/pageerror capture is what turns a blank "Plane didn't start up
// correctly" screen into an actionable stack trace.
import { launch, WEB, shot, settle, mkLogger } from "./lib.mjs";

const log = mkLogger();
const path = process.argv[2] ?? "/hrms/";
const { ctx, page } = await launch();

const seen = [];
page.on("response", async (res) => {
  const url = res.url();
  if (!url.includes("/api/")) return;
  let body = "";
  try {
    body = (await res.text()).replace(/\s+/g, " ").slice(0, 220);
  } catch {
    body = "<no body>";
  }
  seen.push(`${res.status()} ${res.request().method()} ${url.replace("http://localhost:8000", "")}\n     ${body}`);
});

page.on("console", (msg) => {
  const t = msg.type();
  if (t !== "error" && t !== "warning") return;
  log.log(`[console.${t}] ${msg.text().slice(0, 400)}`);
});
page.on("pageerror", (err) => {
  log.log(`[pageerror] ${(err && (err.stack || err.message)) || String(err)}`.slice(0, 1500));
});

await page.goto(WEB + path, { waitUntil: "domcontentloaded" });
// The SPA boots client-side (root route is `ssr: false`), so an immediate
// settle() can observe an empty, already-stable DOM and return before the app
// has issued a single request. Wait for either API traffic or real text first.
await page
  .waitForFunction(
    () => (document.body.innerText || "").trim().length > 40,
    null,
    { timeout: 60000 }
  )
  .catch(() => {});
await page.waitForTimeout(4000);
await settle(page, 2500, 30000);
log.log("URL=" + page.url());
for (const s of seen) log.log(s);

// Report what the app actually rendered.
const text = await page.evaluate(() => (document.body.innerText || "").replace(/\s+/g, " ").slice(0, 400));
log.log("\nVISIBLE TEXT: " + text);

await shot(page, "probe-page");
await ctx.close();
