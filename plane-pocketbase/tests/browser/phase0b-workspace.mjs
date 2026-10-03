// Phase 0b — create the first workspace from the instance admin console.
import { launch, ADMIN, shot, describe, settle, writeNet, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

async function step(name, fn) {
  log.log(`\n===== ${name} =====`);
  try {
    await fn();
  } catch (e) {
    log.log(`  !! ${name} FAILED: ${e.message.split("\n")[0]}`);
    await shot(page, `ERR-${name}`).catch(() => {});
    await describe(page).catch(() => {});
  }
}

await step("00-goto-general", async () => {
  await page.goto(ADMIN + "/god-mode/general/", { waitUntil: "domcontentloaded" });
  await settle(page, 1200, 15000);
  log.log("url=" + page.url());
  await shot(page, "03-admin-console", "实例管理台");
});

await step("01-click-create-workspace", async () => {
  const cta = page.getByText("Create workspace", { exact: false }).first();
  await cta.click();
  await page.waitForTimeout(3000);
  await settle(page, 1200, 12000);
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "04-admin-create-workspace", "创建 workspace 表单");
});

writeNet(net, "phase0b-workspace");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
