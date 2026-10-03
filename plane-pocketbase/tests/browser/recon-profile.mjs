// Recon: why does /hrms/profile/<userId>/ crash in ProfileSidebar?
//
// The crash is `Cannot read properties of undefined (reading 'map')` at
// `userProjectsData.project_data.map(...)` (components/profile/sidebar.tsx:151,
// line 223 in the dev-server module). That branch only runs when
// `userProjectsData` is truthy, yet a direct curl of
// /api/workspaces/hrms/user-profile/<id>/ answers 404 — so either the browser
// gets a different response, or SWR is handing back something unexpected.
//
// This records every request the page makes plus the raw body of the
// user-profile call, straight from the page context (same cookies, same origin).
import { launch, WEB, shot, settle, safeText, mkLogger } from "./lib.mjs";

const log = mkLogger();
const userId = process.argv[2];
const { ctx, page } = await launch();

const seen = [];
page.on("response", async (res) => {
  const u = res.url();
  if (!/\/api\/workspaces\/hrms\//.test(u)) return;
  seen.push(res.status() + " " + u.replace("http://localhost:3000", ""));
});

await page.goto(`${WEB}/hrms/profile/${userId}/`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(9000);
await settle(page, 1500, 15000);

log.log("TEXT=" + (await safeText(page)).replace(/\s+/g, " ").slice(0, 400));
log.log("--- requests ---");
for (const s of seen) log.log("  " + s);

const probe = await page.evaluate(async (id) => {
  // MUST be absolute. A relative "/api/..." resolves against the page origin
  // (localhost:3000) and is answered by Vite's SPA fallback with index.html —
  // which is exactly the false reading the first version of this recon produced.
  const out = {};
  for (const path of [
    `/api/workspaces/hrms/user-profile/${id}/`,
    `/api/workspaces/hrms/user-stats/${id}/`,
    `/api/workspaces/hrms/user-activity/${id}/?per_page=10`,
  ]) {
    try {
      const r = await fetch(`http://localhost:8000${path}`, { credentials: "include" });
      const text = await r.text();
      out[path] = { status: r.status, ctype: r.headers.get("content-type"), body: text.slice(0, 400) };
    } catch (e) {
      out[path] = { error: String(e) };
    }
  }
  return out;
}, userId);
log.log("--- direct probes from the page ---");
log.log(JSON.stringify(probe, null, 2));

await shot(page, "recon-profile-crash", "个人资料页崩溃现场");
await ctx.close();
