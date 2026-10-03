// Recon: why does the Space app crash on a published link?
//
// Opens /spaces/issues/<anchor>, records every console message, page error and
// request, then dumps the DOM so we can see how far it got.
import { launch, SPACE, shot, settle, mkLogger, safeText } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();
const anchor = process.argv[2];

const errors = [];
page.on("console", (m) => {
  if (m.type() === "error" || m.type() === "warning") errors.push(`[console.${m.type()}] ${m.text()}`);
});
page.on("pageerror", (e) => errors.push(`[pageerror] ${e.message}\n${(e.stack || "").split("\n").slice(0, 6).join("\n")}`));
page.on("requestfailed", (r) => errors.push(`[requestfailed] ${r.method()} ${r.url()} :: ${r.failure()?.errorText}`));
page.on("response", (res) => {
  if (res.status() >= 400) errors.push(`[http ${res.status()}] ${res.request().method()} ${res.url()}`);
});

const url = `${SPACE}/spaces/issues/${anchor}`;
log.log("opening " + url);
await page.goto(url, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(10000);
await settle(page, 2000, 20000);

log.log("url=" + page.url());
log.log("TEXT=" + (await safeText(page)).replace(/\s+/g, " ").slice(0, 500));
log.log("\n--- console / errors ---");
for (const e of errors) log.log(e);
await shot(page, "recon-space-crash", "Space 崩溃现场");
await ctx.close();
