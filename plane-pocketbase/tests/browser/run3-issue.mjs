// Run 3 — work items (issues): create one through the UI, verify it renders,
// then reload to prove it persisted.
//
// The project id is discovered by clicking the project card in the workspace
// project list rather than hard-coded, so the script works on any fresh
// database.
import { launch, WEB, shot, settle, describe, writeNet, mkLogger, waitForShell } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const TITLE = "验证 PocketBase 后端：创建第一个工作项";
const DESCRIPTION = "本工作项由浏览器真实操作创建，用于验证 Plane 前端 + PocketBase 后端的端到端链路。";

let projectId = null;

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

async function openProjectIssues() {
  await page.goto(`${WEB}/hrms/projects/`, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(4000);
  // The sidebar also contains `/hrms/projects/`, so pick the anchor whose href
  // actually carries a project UUID — that is the project card.
  const href = await page.evaluate(() => {
    const anchors = [...document.querySelectorAll('a[href*="/projects/"]')];
    const card = anchors.find((el) => /\/projects\/[0-9a-f-]{36}/.test(el.getAttribute("href") || ""));
    return card ? card.getAttribute("href") : null;
  });
  if (!href) throw new Error("no project card anchor found on the projects page");
  const m = href.match(/\/projects\/([0-9a-f-]{36})/);
  projectId = m ? m[1] : null;
  log.log(`project href=${href} projectId=${projectId}`);

  await page.goto(`${WEB}/hrms/projects/${projectId}/issues`, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(6000);
  await settle(page, 2500, 25000);
}

await step("C1-open-project-work-items", async () => {
  await openProjectIssues();
  log.log("TEXT=" + (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ").slice(0, 400));
  await shot(page, "14-work-items-empty", "项目 → Work items（空态）");
});

await step("C2-add-work-item", async () => {
  if (!projectId) throw new Error("project id was not resolved; skipping");
  const add = page.getByText("Add work item", { exact: false }).first();
  await add.waitFor({ state: "visible", timeout: 20000 });
  await add.click();
  await page.waitForTimeout(3000);
  await settle(page, 1200, 12000);
  await page.fill("#name", TITLE);
  await settle(page, 800, 6000);
  await shot(page, "15-work-item-composer-filled", "填写工作项标题");
  await describe(page);
});

await step("C3-save-work-item", async () => {
  if (!projectId) throw new Error("project id was not resolved; skipping");
  await page.click("button[type=submit]");
  await page.waitForTimeout(6000);
  await settle(page, 2000, 20000);
  const text = (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ");
  log.log("HAS_TITLE=" + text.includes("验证 PocketBase 后端"));
  await shot(page, "16-work-item-created", "工作项已创建并出现在列表中");
});

await step("C4-open-work-item", async () => {
  if (!projectId) throw new Error("project id was not resolved; skipping");
  await page.getByText(TITLE, { exact: false }).first().click();
  await page.waitForTimeout(5000);
  await settle(page, 2000, 20000);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ").slice(0, 500));
  await shot(page, "17-work-item-detail", "工作项详情面板");
});

await step("C5-add-description", async () => {
  if (!projectId) throw new Error("project id was not resolved; skipping");
  // The detail panel's description is a rich-text editor; click it and type.
  const editable = page.locator('[contenteditable="true"]').first();
  if (await editable.count()) {
    await editable.click();
    await page.waitForTimeout(800);
    await page.keyboard.type(DESCRIPTION);
    await page.waitForTimeout(1500);
    // Persist: blur so the editor's onBlur save fires.
    await page.keyboard.press("Escape");
    await page.waitForTimeout(3000);
    await settle(page, 1500, 15000);
  } else {
    log.log("no contenteditable found");
  }
  await shot(page, "18-work-item-description", "补充工作项描述");
});

await step("C6-reload-persistence", async () => {
  await page.goto(`${WEB}/hrms/projects/${projectId}/issues`, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(6000);
  await settle(page, 2500, 25000);
  const text = (await page.evaluate(() => document.body.innerText)).replace(/\s+/g, " ");
  log.log("AFTER_RELOAD_HAS_TITLE=" + text.includes("验证 PocketBase 后端"));
  log.log("TEXT=" + text.slice(0, 400));
  await shot(page, "19-work-item-after-reload", "刷新后工作项仍然存在（持久化验证）");
});

writeNet(net, "run3-issue");
log.log("\nFINAL_URL=" + page.url());
log.log("PROJECT_ID=" + projectId);
await ctx.close();
