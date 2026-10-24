// Run 1 — bootstrap: instance admin (god-mode) -> web onboarding -> first workspace.
//
// Idempotent: on an already-configured instance it signs the admin in instead
// of signing up, so the script can be re-run against the same database.
import { launch, ADMIN, WEB, shot, describe, settle, writeNet, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const ADMIN_EMAIL = "admin@hrms.local";
const ADMIN_PASSWORD = "Plane12345!";
const WS_NAME = "hrms";
const WS_SLUG = "hrms";

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

// ---------------------------------------------------------------- 1. admin
await step("A1-admin-signup", async () => {
  await page.goto(ADMIN + "/god-mode/", { waitUntil: "domcontentloaded" });
  await settle(page, 1200, 15000);
  log.log("url=" + page.url());

  const signupForm = page.locator("#first_name");
  if (await signupForm.count()) {
    log.log("fresh instance -> sign-up path");
    await shot(page, "01-admin-setup-empty", "实例管理员注册页");
    await page.fill("#first_name", "Wilber");
    await page.fill("#last_name", "Wright");
    await page.fill("#email", ADMIN_EMAIL);
    await page.fill("#company_name", "HRMS");
    await page.fill("#password", ADMIN_PASSWORD);
    await page.fill("#confirm_password", ADMIN_PASSWORD);
    await settle(page, 600, 5000);
    await shot(page, "02-admin-setup-filled", "填写实例管理员信息");
    await page.click("button[type=submit]");
  } else {
    log.log("instance already set up -> sign-in path");
    await page.fill("#email", ADMIN_EMAIL);
    await page.fill("#password", ADMIN_PASSWORD);
    await page.click("button[type=submit]");
  }
  await page.waitForTimeout(7000);
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  await shot(page, "03-admin-console", "实例管理台 General");
});

// ---------------------------------------------------------------- 2. onboarding
await step("A2-web-onboarding", async () => {
  await page.goto(WEB + "/onboarding/", { waitUntil: "domcontentloaded" });
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  await describe(page);
  // The profile step renders immediately — there is no separate "entry" screen.
  await shot(page, "04-onboarding-profile", "Onboarding：创建个人资料");
});

await step("A3-profile", async () => {
  const name = page.locator("#first_name, input[name=first_name]").first();
  if (await name.count()) {
    // The step only renders `first_name` (labelled "full name"); `last_name` is
    // prefilled from the account and submitted untouched, so "Wilber" here keeps
    // the display name as "Wilber Wright" rather than duplicating the surname.
    await name.fill("Wilber");
    await settle(page, 500, 4000);
    await shot(page, "05-onboarding-profile-filled", "填写个人资料");
    await page.click("button[type=submit]");
    // The workspace step is a lazily-loaded route chunk. On a cold cache (first
    // ever visit in this profile) it can take much longer than the old fixed
    // 4.5s, which made A4 fail with a 30s fill timeout against a spinner.
    // Wait for the actual element instead of the clock; retry the click once in
    // case it landed while react-hook-form was still settling.
    const wsName = page.locator('input[name="name"]').first();
    try {
      await wsName.waitFor({ state: "visible", timeout: 45000 });
    } catch {
      log.log("  workspace form did not appear; retrying submit once");
      await page.click("button[type=submit]").catch(() => {});
      await wsName.waitFor({ state: "visible", timeout: 45000 });
    }
  }
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "06-onboarding-workspace-form", "创建工作区表单");
});

await step("A4-workspace", async () => {
  await page.fill("#name, input[name=name]", WS_NAME);
  await settle(page, 700, 5000);
  await page.fill("#slug, input[name=slug]", WS_SLUG);
  await settle(page, 700, 5000);
  const justMe = page.getByText("Just myself", { exact: false }).first();
  if (await justMe.count()) {
    await justMe.click().catch(() => {});
    await page.waitForTimeout(700);
  }
  await settle(page, 700, 5000);
  await shot(page, "07-workspace-form-filled", "填写工作区信息");
  await describe(page);
  await page.click("button[type=submit]");
  await page.waitForTimeout(10000);
  await settle(page, 1800, 20000);
  log.log("url=" + page.url());
  await shot(page, "08-workspace-home", "工作区首页");
  await describe(page);
});

writeNet(net, "run1-bootstrap");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
