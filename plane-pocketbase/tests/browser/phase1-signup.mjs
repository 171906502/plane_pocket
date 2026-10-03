// Phase 1 — account initialisation: sign up, set profile, create the workspace.
// Runs against a freshly migrated, empty PocketBase database.
import { launch, WEB, shot, describe, settle, writeNet, randomEmail, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const email = randomEmail("wf");
const password = "Plane12345!";
log.log("EMAIL=" + email);

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

// 1. landing ---------------------------------------------------------------
await step("01-landing", async () => {
  await page.goto(WEB + "/", { waitUntil: "domcontentloaded" });
  await settle(page, 900, 15000);
  log.log("url=" + page.url());
  await shot(page, "01-instance-welcome", "空实例欢迎页");
});

// 2. welcome -> sign-up form ----------------------------------------------
await step("01b-get-started", async () => {
  const cta = page.getByText("Get started", { exact: false }).first();
  if (await cta.count()) {
    await cta.click();
    await page.waitForTimeout(3500);
    await settle(page);
  }
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "02-signup-email", "登录/注册页：输入邮箱");
});

// 3. email -> password -----------------------------------------------------
await step("02-email", async () => {
  await page.fill("#email", email);
  await settle(page, 400, 3000);
  await shot(page, "03-signup-email-filled", "已填邮箱");
  await page.click("button[type=submit]");
  await page.waitForTimeout(3000);
  await settle(page);
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "04-signup-password", "设置密码");
});

// 4. password -> profile ---------------------------------------------------
await step("03-password", async () => {
  const pw = page.locator("input[type=password]");
  log.log("password inputs=" + (await pw.count()));
  await pw.nth(0).fill(password);
  if ((await pw.count()) > 1) await pw.nth(1).fill(password);
  await settle(page, 400, 3000);
  await shot(page, "05-signup-password-filled", "已填密码");
  await page.click("button[type=submit]");
  await page.waitForTimeout(4000);
  await settle(page);
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "06-onboarding-profile", "创建个人资料");
});

// 5. profile -> workspace --------------------------------------------------
await step("04-profile", async () => {
  const name = page.locator("#first_name, input[name=first_name]").first();
  await name.fill("Alice Chen");
  await settle(page, 400, 3000);
  await shot(page, "07-profile-filled", "已填姓名");
  await page.click("button[type=submit]");
  await page.waitForTimeout(4000);
  await settle(page);
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "08-workspace-form", "创建工作区");
});

// 6. workspace -> app home -------------------------------------------------
await step("05-workspace", async () => {
  await page.fill("#name, input[name=name]", "hrms");
  await settle(page, 600, 5000);
  await page.fill("#slug, input[name=slug]", "hrms");
  await settle(page, 600, 5000);
  const justMe = page.getByText("Just myself", { exact: false }).first();
  if (await justMe.count()) {
    await justMe.click().catch(() => {});
    await page.waitForTimeout(600);
  }
  await settle(page, 600, 5000);
  await shot(page, "09-workspace-filled", "已填工作区表单");
  await describe(page);
  await page.click("button[type=submit]");
  // Workspace creation triggers several follow-up fetches; give it room.
  await page.waitForTimeout(9000);
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  await shot(page, "10-workspace-home", "工作区创建完成");
});

writeNet(net, "phase1-signup");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
