// Run 2 — inside the workspace: create the first project, then inspect it.
import { launch, WEB, shot, describe, settle, writeNet, mkLogger, waitForShell, clickText, waitDialog } from "./lib.mjs";

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

await step("B0-workspace-home", async () => {
  await page.goto(WEB + "/hrms/", { waitUntil: "domcontentloaded" });
  await waitForShell(page);
  await settle(page, 1200, 15000);
  log.log("url=" + page.url());
  await shot(page, "09-workspace-home-loaded", "工作区首页（已加载）");
});

await step("B1-dismiss-tour-modal", async () => {
  const dismiss = page.getByText("No thanks, I will explore it myself", { exact: false }).first();
  if (await dismiss.count()) {
    await dismiss.click();
    log.log("clicked dismiss");
    await page.waitForTimeout(2000);
    await waitForShell(page);
    await settle(page, 1200, 15000);
  } else {
    log.log("no welcome modal present");
  }
  log.log("url=" + page.url());
  await shot(page, "10-workspace-home-clean", "关闭欢迎弹窗后的工作区首页");
});

await step("B2-open-add-project", async () => {
  await clickText(page, "Add project");
  await page.waitForTimeout(2500);
  const hasDialog = await waitDialog(page);
  log.log("dialog=" + hasDialog + " url=" + page.url());
  await settle(page, 1200, 12000);
  await describe(page);
  await shot(page, "11-project-form", "新建项目表单");
});

await step("B3-fill-project", async () => {
  await page.fill("#name", "hrms");
  await settle(page, 700, 5000);
  await page.fill("#identifier", "HRMS");
  await settle(page, 700, 5000);
  await page.fill("#description", "HRMS 项目：用于验证 Plane + PocketBase 全链路。");
  await settle(page, 700, 5000);
  await shot(page, "12-project-form-filled", "填写项目名称与 ID");
  await describe(page);
});

await step("B4-submit-project", async () => {
  await page.click("button[type=submit]");
  await page.waitForTimeout(8000);
  await waitForShell(page);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "13-project-created", "项目创建成功");
  await describe(page);
});

writeNet(net, "run2-project");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
