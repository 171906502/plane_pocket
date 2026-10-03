// Phase 0 — instance bootstrap through the god-mode admin app.
// A brand-new Plane instance has no workspaces, so the web app's root shows
// "Welcome to Plane -> Get started", which links here. The instance admin must
// exist before any workspace can be created.
import { launch, ADMIN, WEB, shot, describe, settle, writeNet, randomEmail, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const email = randomEmail("god");
const password = "Plane12345!";
log.log("ADMIN_EMAIL=" + email);

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

await step("00-admin-form", async () => {
  await page.goto(ADMIN + "/god-mode/", { waitUntil: "domcontentloaded" });
  await settle(page, 1200, 15000);
  log.log("url=" + page.url());
  await shot(page, "00-admin-setup-empty", "实例管理员注册页（空实例）");
});

await step("01-admin-fill", async () => {
  await page.fill("#first_name", "Wilber");
  await page.fill("#last_name", "Wright");
  await page.fill("#email", email);
  await page.fill("#company_name", "HRMS");
  await page.fill("#password", password);
  await page.fill("#confirm_password", password);
  await settle(page, 600, 5000);
  await shot(page, "01-admin-setup-filled", "填写实例管理员信息");
  await describe(page);
});

await step("02-admin-submit", async () => {
  await page.click("button[type=submit]");
  await page.waitForTimeout(7000);
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  await describe(page);
  await shot(page, "02-admin-after-setup", "提交后的管理员界面");
});

writeNet(net, "phase0-admin");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
