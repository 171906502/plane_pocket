// Run 3 — enter the project the way a user does (sidebar click) and tour
// Overview / Work items / Views / Pages.
import { launch, WEB, shot, describe, settle, writeNet, mkLogger, waitForShell } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const WS = "hrms";

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

await step("C1-open-workspace", async () => {
  await page.goto(`${WEB}/${WS}/`, { waitUntil: "domcontentloaded" });
  await waitForShell(page);
  await settle(page, 1500, 20000);
  log.log("url=" + page.url());
  await shot(page, "15-workspace-home", "工作区首页");
});

await step("C2-click-project", async () => {
  // Target the sidebar's project link by href — a text match would also hit the
  // workspace switcher entry, which has the same name.
  const link = page.locator('a[href*="/projects/"]').first();
  await link.waitFor({ state: "visible", timeout: 20000 });
  const href = await link.getAttribute("href");
  log.log("clicking href=" + href);
  await link.click();
  await page.waitForTimeout(6000);
  await waitForShell(page);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "16-project-opened", "从侧边栏进入项目");
  await describe(page);
});

await step("C3-project-work-items", async () => {
  await page.goto(`${WEB}/${WS}/projects/5d8df2bb-de18-4986-91d3-47e4f3613b7e/issues`, {
    waitUntil: "domcontentloaded",
  });
  await waitForShell(page);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "17-project-work-items", "项目 Work items");
});

await step("C4-project-views", async () => {
  await page.goto(`${WEB}/${WS}/projects/5d8df2bb-de18-4986-91d3-47e4f3613b7e/views`, {
    waitUntil: "domcontentloaded",
  });
  await waitForShell(page);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "18-project-views", "项目 Views");
});

await step("C5-project-pages", async () => {
  await page.goto(`${WEB}/${WS}/projects/5d8df2bb-de18-4986-91d3-47e4f3613b7e/pages`, {
    waitUntil: "domcontentloaded",
  });
  await waitForShell(page);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "19-project-pages", "项目 Pages");
});

writeNet(net, "run3-project-tour");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
