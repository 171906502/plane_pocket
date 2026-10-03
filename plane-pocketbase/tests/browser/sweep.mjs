// Multi-route sweep: visit each project sub-route and report what renders plus
// any failed API call, in one browser session. Used to decide which routes are
// worth scripting into the documented workflow.
import { launch, WEB, shot, settle, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();

const projectId = process.argv[2];
const routes = process.argv.slice(3);

for (const route of routes) {
  // Routes starting with `/hrms/` are absolute; everything else is a
  // project-scoped sub-route appended to the project URL.
  const url = route.startsWith("/hrms/")
    ? `${WEB}${route}`
    : `${WEB}/hrms/projects/${projectId}${route}`;
  const failures = [];
  const onResp = (res) => {
    if (res.status() >= 400 && /:(3000|8000)\//.test(res.url())) {
      failures.push(`${res.status()} ${res.request().method()} ${res.url().replace("http://localhost:8000", "")}`);
    }
  };
  page.on("response", onResp);
  try {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 45000 }).catch(() => {});
    await page.waitForTimeout(5000);
    await settle(page, 2000, 18000);
    const text = (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ");
    const name = route.replace(/[^a-z0-9]+/gi, "-").replace(/^-|-$/g, "") || "root";
    log.log(`\n### ${route}  -> ${page.url()}`);
    log.log(`TEXT: ${text.slice(0, 320)}`);
    log.log(`FAILURES: ${failures.length ? failures.join(" | ") : "(none)"}`);
    await shot(page, `sweep-${name}`);
  } catch (e) {
    log.log(`\n### ${route}  !! ${e.message.split("\n")[0]}`);
    await shot(page, `sweep-err-${route.replace(/[^a-z0-9]+/gi, "-")}`);
  } finally {
    page.off("response", onResp);
  }
}

await ctx.close();
