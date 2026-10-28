// Verify the collaboration write-back: typing in the page editor must PATCH
// `…/pages/{id}/description/` once the 1.5s debounce elapses, carrying a full
// `description_html`.
//
// Run against the single-port binary:
//   PLANE_WEB_URL=http://localhost:8000 node verify-collab-writeback.mjs <pageId> [projectId]
import { launch, WEB, shot, settle, mkLogger, writeNet } from "./lib.mjs";

const log = mkLogger();
// ⚠️ Both path segments are Plane **UUIDs**, not PocketBase record ids.
// `ResolveProject` looks the project up with `uuid = {:id}` (internal/middleware
// /scope.go), so handing it the 15-char record id makes every project-scoped
// call 404 and the page renders "Project not found" — which reads exactly like
// a broken frontend build. Get the uuids from the database:
//   sqlite3 .fe-run/data.db "select id, uuid, name from projects;"
//   sqlite3 .fe-run/data.db "select id, uuid, name from pages;"
const PAGE_ID = process.argv[2] || "32be4a84-eafb-429d-b875-e9e7abe8c8dd";
const PROJECT_ID = process.argv[3] || "a0f56f55-c542-4fb0-913f-9d30d330f4e2";
const URL = `${WEB}/hrms/projects/${PROJECT_ID}/pages/${PAGE_ID}/`;
const MARKER = `WB-${Date.now().toString().slice(-6)}`;
const EMAIL = process.env.PLANE_EMAIL || "admin@hrms.local";
const PASSWORD = process.env.PLANE_PASSWORD || "Plane12345!";

const anyEditable = (page) => page.locator('[contenteditable="true"]').first();

// The persistent profile may still hold a session for a *different* account
// (this database has two workspaces, each with its own user). Landing on the
// page then yields "Project not found" instead of an editor, which looks like
// a broken build. Always start from a clean cookie jar and sign in explicitly.
async function freshLogin(ctx, page) {
  await ctx.clearCookies();
  log.log(`[auth] 清空 cookie，登录 ${EMAIL}`);
  await page.goto(`${WEB}/`, { waitUntil: "domcontentloaded" });
  await settle(page, 900, 20000);

  if (await page.locator("#email").count()) {
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
  } else {
    log.log("[auth] 已在登录态");
  }
  await settle(page, 900, 20000);
  log.log(`[auth] 登录后 url=${page.url()}`);
}

log.log(`[url] ${URL}`);
log.log(`[marker] ${MARKER}`);

const { ctx, page, net } = await launch();

try {
  await freshLogin(ctx, page);

  await page.goto(URL, { waitUntil: "domcontentloaded" });
  await settle(page, 900, 20000);

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
