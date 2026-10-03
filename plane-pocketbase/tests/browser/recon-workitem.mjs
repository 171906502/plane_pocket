// Recon: open a project's Work items view, click "Add work item", and dump the
// resulting DOM so the creation flow can be scripted precisely.
import { launch, WEB, shot, settle, describe, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();

const PROJECT = process.argv[2] ?? "5d8df2bb-de18-4986-91d3-47e4f3613b7e";
const base = `${WEB}/hrms/projects/${PROJECT}`;

async function step(name, fn) {
  log.log(`\n===== ${name} =====`);
  try {
    await fn();
  } catch (e) {
    log.log(`  !! ${name} FAILED: ${e.message.split("\n")[0]}`);
    await shot(page, `RECON-ERR-${name}`).catch(() => {});
  }
}

await step("open-work-items", async () => {
  await page.goto(`${base}/issues`, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(5000);
  await settle(page, 2000, 20000);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ").slice(0, 500));
  await shot(page, "recon-01-work-items");
});

await step("click-add-work-item", async () => {
  const btn = page.getByText("Add work item", { exact: false }).first();
  await btn.waitFor({ state: "visible", timeout: 20000 });
  await btn.click();
  await page.waitForTimeout(4000);
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ").slice(0, 900));
  await shot(page, "recon-02-add-work-item");
  await describe(page);
});

writeNetless();
function writeNetless() {}

await ctx.close();
