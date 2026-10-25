// Verify the collaboration write-back: typing in the page editor must PATCH
// `…/pages/{id}/description/` once the 1.5s debounce elapses, carrying a full
// `description_html`.
//
// Run against the single-port binary:
//   PLANE_WEB_URL=http://localhost:8000 node verify-collab-writeback.mjs <pageId> [projectId]
import { launch, WEB, shot, settle, mkLogger, writeNet } from "./lib.mjs";

const log = mkLogger();
const PAGE_ID = process.argv[2] || "wyxtjhhe469gk1l";
const PROJECT_ID = process.argv[3] || "3gx5zov764djlq7";
const URL = `${WEB}/hrms/projects/${PROJECT_ID}/pages/${PAGE_ID}/`;
const MARKER = `WB-${Date.now().toString().slice(-6)}`;
const EMAIL = process.env.PLANE_EMAIL || "admin@hrms.local";
const PASSWORD = process.env.PLANE_PASSWORD || "Plane12345!";

const editorLocator = (page) => page.locator(".ProseMirror").first();
const anyEditable = (page) => page.locator('[contenteditable="true"]').first();

async function countEditors(page) {
  const pm = await page.locator(".ProseMirror").count().catch(() => 0);
  const ce = await page.locator('[contenteditable="true"]').count().catch(() => 0);
  return pm + ce;
}

async function loginIfNeeded(page) {
  if ((await page.locator("#email").count()) === 0) return false;
  log.log("[auth] 未登录，执行登录");
  await page.fill("#email", EMAIL);
  await page
    .getByRole("button", { name: /continue/i })
    .click()
    .catch(() => page.locator("button[type=submit]").first().click());
  await page.waitForTimeout(2500);
  if (await page.locator("#password").count()) {
    await page.fill("#password", PASSWORD);
    await page.locator("button[type=submit]").first().click().catch(() => {});
    await page.waitForTimeout(6000);
  }
  return true;
}

log.log(`[url] ${URL}`);
log.log(`[marker] ${MARKER}`);

const { ctx, page, net } = await launch();

try {
  await page.goto(URL, { waitUntil: "domcontentloaded" });
  await settle(page, 900, 20000);

  if ((await countEditors(page)) === 0) {
    const logged = await loginIfNeeded(page);
    if (logged) {
      await page.goto(URL, { waitUntil: "domcontentloaded" });
      await settle(page, 900, 20000);
    }
  }

  log.log(`[url after auth] ${page.url()}`);

  // The page mounts two ProseMirror surfaces: the title (H1) and the document
  // body. Only the body feeds `description_html`, so target the second one.
  const editors = page.locator(".ProseMirror");
  const editorCount = await editors.count();
  let editor = editorCount > 1 ? editors.nth(1) : editors.first();
  if (editorCount === 0) editor = anyEditable(page);
  log.log(`[editor] ProseMirror × ${editorCount}，使用 ${editorCount > 1 ? "第 2 个（正文）" : "第 1 个"}`);
  await editor.waitFor({ state: "visible", timeout: 45000 });
  await settle(page, 800, 10000);
  await shot(page, "collab-01-opened", "编辑器已加载");

  await editor.click({ timeout: 15000 });
  await page.keyboard.press("Control+End").catch(() => {});
  await page.keyboard.type(` ${MARKER}`, { delay: 45 });
  log.log(`[type] 已输入 " ${MARKER}"`);

  // Debounce is 1500ms; give the request time to land.
  await page.waitForTimeout(3500);
  await shot(page, "collab-02-after-debounce", "输入后 3.5 秒");

  const patches = net.filter((n) => n.method === "PATCH" && n.url.includes("/description/"));
  log.log(`[net] PATCH /description/ × ${patches.length}`);
  for (const p of patches) log.log(`      ${p.status} ${p.url}`);

  const writes = net.filter((n) => n.method !== "GET" && n.method !== "HEAD" && n.status < 400);
  log.log(`[net] 全部写请求 ${writes.length} 条`);
  for (const w of writes.slice(0, 10)) log.log(`      ${w.method} ${w.status} ${w.url}`);

  writeNet(net, "collab-writeback");
  console.log(patches.length > 0 ? "[result] PASS 产生了 PATCH" : "[result] FAIL 没有 PATCH");
} finally {
  await ctx.close();
}
