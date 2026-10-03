// Run 5 — publish the project and open the resulting public link in the Space app.
//
// This is the last leg of the workflow and it crosses all three frontends:
//   web (3000)   -> click "Publish project" -> POST .../project-deploy-boards/
//   space (3002) -> /spaces/issues/<anchor> renders the read-only project view
//
// The published URL is not guessed: it is read straight off the anchor the modal
// renders (`SPACE_BASE_URL + SPACE_BASE_PATH + /issues/<anchor>`), which is the
// exact link the product gives the user to copy.
import { launch, WEB, SPACE, shot, settle, describe, writeNet, mkLogger, safeText, resetPage } from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const projectId = process.argv[2];

async function step(name, fn) {
  log.log(`\n===== ${name} =====`);
  try {
    await Promise.race([fn(), new Promise((_, rej) => setTimeout(() => rej(new Error("step watchdog fired")), 90000))]);
  } catch (e) {
    log.log(`  !! ${name} FAILED: ${e.message.split("\n")[0]}`);
    await shot(page, `ERR-${name}`).catch(() => {});
    await describe(page).catch(() => {});
    await resetPage(page);
  }
}

async function goto(url, settleMs = 20000) {
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page
    .waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 })
    .catch(() => {});
  await page.waitForTimeout(5000);
  await settle(page, 2200, settleMs);
}

const text = async () => (await safeText(page)).replace(/\s+/g, " ").slice(0, 400);

await step("E1-open-project", async () => {
  await goto(`${WEB}/hrms/projects/${projectId}/issues/`);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await text()));
  await shot(page, "26-publish-entry", "项目页（Publish 入口所在）");
});

// Open the project actions dropdown and click "Publish project".
//
// Recon findings (recon-publish.mjs) — there is no directly clickable trigger:
//   * the three `Toggle quick actions menu` buttons belong to the sidebar
//     project row. They are rendered with `pointer-events: none` and an
//     `opacity: 0` parent, i.e. they only become interactive while the row is
//     hovered. Playwright's `isVisible()` returns true for them (opacity is not
//     part of its visibility test), so a naive click just times out.
//   * the tab-navigation `More actions` button renders at 0x0 in this layout.
//
// So: hover the row first (the real user gesture that reveals the button), then
// click it. Coordinates come from the button's own bounding box, moved left into
// the row body.
await step("E2-open-publish-modal", async () => {
  const cands = page.locator('button[aria-label*="action" i]');
  const n = await cands.count();
  const menuItem = () => page.getByText("Publish project", { exact: false }).first();

  let opened = false;
  for (let i = 0; i < n && !opened; i++) {
    const c = cands.nth(i);
    const box = await c.boundingBox();
    if (!box || box.width === 0) continue; // 0x0 => not rendered, skip
    // Move into the project row to the left of the button, which is what makes
    // the button interactive.
    await page.mouse.move(Math.max(box.x - 60, 5), box.y + box.height / 2);
    await page.waitForTimeout(600);
    try {
      await c.click({ timeout: 5000 });
    } catch (e) {
      log.log(`candidate ${i}: ${e.message.split("\n")[0]}`);
      continue;
    }
    opened = await menuItem()
      .isVisible({ timeout: 3000 })
      .catch(() => false);
    log.log(`candidate ${i}: clicked at (${Math.round(box.x)},${Math.round(box.y)}), menu open=${opened}`);
    if (!opened) {
      await shot(page, `diag-menu-${i}`, `candidate ${i} 打开后的菜单`);
      await page.keyboard.press("Escape").catch(() => {});
      await page.waitForTimeout(400);
    }
  }
  if (!opened) throw new Error(`project actions menu never opened (${n} candidates)`);

  await menuItem().click();
  log.log("clicked 'Publish project'");
  await page.waitForTimeout(2500);
  await settle(page, 1200, 12000);
  await shot(page, "27-publish-modal", "Publish project 弹窗");
  log.log("TEXT=" + (await text()));
});

await step("E3-publish", async () => {
  const readLink = () =>
    page.evaluate(() => {
      const a = [...document.querySelectorAll("a[href]")].find((x) => /\/spaces\/issues\//.test(x.getAttribute("href") || ""));
      return a ? a.getAttribute("href") : null;
    });

  // The script is re-runnable. When the project is already published the modal
  // renders the existing link next to an "Unpublish" button, and there is no
  // confirmation step — clicking it immediately DELETEs the board. We do that
  // on purpose so every run performs a *real* POST with the payload under test
  // instead of silently reusing a row created by an older build (the previous
  // run's board had `view_props` missing, which is exactly the bug being
  // verified here). Django's DELETE soft-deletes, so the anchor is freed.
  const unpublish = page.getByRole("button", { name: /^Unpublish$/ }).first();
  if (await unpublish.isVisible({ timeout: 3000 }).catch(() => false)) {
    log.log("already published; unpublishing first so this run POSTs a fresh board");
    await unpublish.click();
    await page.waitForTimeout(3500);
    await settle(page, 1200, 12000);
    log.log("link after unpublish=" + (await readLink()));
  }

  const publish = page.getByRole("button", { name: /^Publish$/ }).first();
  await publish.waitFor({ state: "visible", timeout: 15000 });
  await publish.click();
  log.log("clicked Publish");
  await page.waitForTimeout(4000);
  await settle(page, 1500, 15000);

  const href = await readLink();
  await shot(page, "28-published-link", "发布成功，显示公开链接");
  log.log("publishLink=" + href);
  if (!href) throw new Error("published link not found in the modal");
  globalThis.__publishHref = href;
  log.log("TEXT=" + (await text()));
});

await step("E4-space-view", async () => {
  const href = globalThis.__publishHref;
  if (!href) throw new Error("no published link captured");
  const anchor = href.split("/").filter(Boolean).pop();
  const url = href.startsWith("http") ? href : SPACE + href;
  log.log("opening " + url);

  // Hard evidence for the layout fix. `apps/space/app/issues/[anchor]/layout.tsx`
  // only calls `updateLayoutOptions` `if (response.view_props)`, and the navbar
  // builds its acceptable-layout list from the same key — so a null
  // `view_props` renders the Space shell with no issue list at all. Read the
  // exact payload the Space layout consumes, straight from the API origin the
  // Space app is configured with (VITE_API_BASE_URL).
  const settings = await page.evaluate(async (a) => {
    try {
      const r = await fetch(`http://localhost:8000/api/public/anchor/${a}/settings/`);
      const j = await r.json();
      return { status: r.status, anchor: j.anchor, view_props: j.view_props };
    } catch (e) {
      return { error: String(e) };
    }
  }, anchor);
  log.log("settings=" + JSON.stringify(settings));

  await goto(url);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await text()));
  await shot(page, "29-space-published-issues", "Space：公开的 Issues 列表");
});

await step("E5-space-issue-detail", async () => {
  // Open the read-only work-item detail.
  //
  // Target the LINK, not the identifier text. In
  // `components/issues/issue-layouts/list/block.tsx` the `HRMS-1` identifier is
  // a plain <div> sibling; only the issue NAME is wrapped in the Link that
  // carries `id={`issue-${issue.id}`}` and `to={`?${queryParam}`}` with
  // `peekId` set. Clicking the identifier just hovers the row — which is what
  // the first attempt at this step captured.
  //
  // Verified against the live DOM by recon-peek.mjs:
  //   <a id="issue-05d155c4-…" href="…?board=list&peekId=05d155c4-…">验证 PocketBase 后端…</a>
  const link = page.locator('a[id^="issue-"]').first();
  await link.waitFor({ state: "visible", timeout: 20000 });
  await link.click();
  log.log("clicked the issue link in Space");
  await page.waitForTimeout(4000);
  await settle(page, 1500, 12000);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await text()));
  await shot(page, "30-space-issue-detail", "Space：工作项详情（只读）");
  if (!/[?&]peekId=/.test(page.url())) {
    throw new Error("read-only peek panel did not open (no peekId in URL)");
  }
});

writeNet(net, "run5-publish-space");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
