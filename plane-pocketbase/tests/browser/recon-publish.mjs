// Recon: what exactly happens when we try to open the project actions menu?
//
// run5 could not reach the "Publish project" menu item. This walks every
// candidate trigger, reports its geometry/opacity, clicks it, and screenshots
// whatever appears — so the next fix is based on observation, not guesswork.
import { launch, WEB, shot, settle, mkLogger, safeText, resetPage } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();
const projectId = process.argv[2];

await page.goto(`${WEB}/hrms/projects/${projectId}/issues/`, { waitUntil: "domcontentloaded" });
await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
await page.waitForTimeout(6000);
await settle(page, 2000, 20000);
log.log("TEXT=" + (await safeText(page)).replace(/\s+/g, " ").slice(0, 250));

const info = await page.evaluate(() => {
  const out = [];
  document.querySelectorAll('button[aria-label*="action" i]').forEach((b, i) => {
    const r = b.getBoundingClientRect();
    const cs = getComputedStyle(b);
    out.push({
      i,
      label: b.getAttribute("aria-label"),
      x: Math.round(r.x),
      y: Math.round(r.y),
      w: Math.round(r.width),
      h: Math.round(r.height),
      opacity: cs.opacity,
      display: cs.display,
      visibility: cs.visibility,
      pointerEvents: cs.pointerEvents,
      parentOpacity: getComputedStyle(b.parentElement || b).opacity,
    });
  });
  return out;
});
log.log("CANDIDATES=" + JSON.stringify(info, null, 1));

const n = await page.locator('button[aria-label*="action" i]').count();
for (let i = 0; i < n; i++) {
  const c = page.locator('button[aria-label*="action" i]').nth(i);
  log.log(`\n--- candidate ${i} ---`);
  try {
    await c.click({ timeout: 5000 });
    log.log("click OK");
  } catch (e) {
    log.log("click FAILED: " + e.message.split("\n")[0]);
  }
  await page.waitForTimeout(1500);
  await settle(page, 800, 6000);
  const t = (await safeText(page)).replace(/\s+/g, " ");
  const has = t.includes("Publish project");
  log.log(`menu has 'Publish project' = ${has}`);
  log.log("TEXT=" + t.slice(0, 300));
  await shot(page, `recon-publish-cand${i}`, `candidate ${i}`);
  await page.keyboard.press("Escape").catch(() => {});
  await page.waitForTimeout(800);
}

await ctx.close();
