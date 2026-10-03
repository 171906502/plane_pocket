// Recon: what does a published Space issue row actually expose, and which
// element opens the read-only peek?
//
// Run 5's E5 clicked the `HRMS-1` identifier, which is a plain <div> sibling of
// the real link (components/issues/issue-layouts/list/block.tsx), so it only
// hovered the row. This dumps the row markup, then tries the link, so E5 can
// target the right thing without another full publish cycle.
//
// Usage: node recon-peek.mjs <anchor>
import { launch, SPACE, shot, settle, safeText, mkLogger } from "./lib.mjs";

const log = mkLogger();
const anchor = process.argv[2];
const { ctx, page } = await launch();

await page.goto(`${SPACE}/spaces/issues/${anchor}/`, { waitUntil: "domcontentloaded" });
await page.waitForTimeout(6000);
await settle(page, 1500, 15000);

const rows = await page.evaluate(() => {
  const out = [];
  for (const el of document.querySelectorAll("a[id^='issue-'], a[href*='peekId']")) {
    const r = el.getBoundingClientRect();
    out.push({
      tag: el.tagName.toLowerCase(),
      id: el.id,
      href: el.getAttribute("href"),
      text: (el.innerText || "").trim().slice(0, 60),
      box: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
    });
  }
  return out;
});
log.log("issue links: " + JSON.stringify(rows, null, 2));

const link = page.locator('a[id^="issue-"]').first();
if ((await link.count()) === 0) {
  log.log("!! no a[id^=issue-] found");
} else {
  await link.click();
  log.log("clicked link");
  await page.waitForTimeout(5000);
  await settle(page, 1500, 15000);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await safeText(page)).replace(/\s+/g, " ").slice(0, 600));
  await shot(page, "recon-peek-after-click", "点击 issue 链接之后");
}

await ctx.close();
