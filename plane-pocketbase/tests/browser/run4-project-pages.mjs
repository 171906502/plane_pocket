// Run 4 — the rest of the project surface: Pages (create one), project settings,
// workspace settings, and the personal "Your work" page.
//
// `Your work` lives at `/profile/` (packages/constants/src/workspace.ts), not at
// `/your-work/` — the latter is a 404 in Plane itself.
//
// NOTE ON THE PAGE EDITOR: after run 3 typed into the work-item description, an
// unbounded `page.evaluate` never came back — Plane's renderer stays busy while
// the rich-text editor is mounted. Every DOM read here therefore goes through
// `safeText`, and the editor step tears the renderer down with `resetPage`
// before anything else runs.
import {
  launch,
  WEB,
  shot,
  settle,
  describe,
  writeNet,
  mkLogger,
  safeText,
  resetPage,
  waitForShell,
  waitNoSpinner,
} from "./lib.mjs";

const log = mkLogger();
const { ctx, page, net } = await launch();

const projectId = process.argv[2];
const PAGE_TITLE = "PocketBase 迁移验证记录";

async function step(name, fn, watchdogMs = 90000) {
  log.log(`\n===== ${name} =====`);
  try {
    await Promise.race([
      fn(),
      new Promise((_, rej) => setTimeout(() => rej(new Error("step watchdog fired")), watchdogMs)),
    ]);
  } catch (e) {
    log.log(`  !! ${name} FAILED: ${e.message.split("\n")[0]}`);
    await shot(page, `ERR-${name}`).catch(() => {});
    await describe(page).catch(() => {});
    await resetPage(page);
  }
}

async function goto(url, settleMs = 20000) {
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page.waitForFunction(() => (document.body.innerText || "").trim().length > 40, null, { timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(5000);
  await settle(page, 2200, settleMs);
}

const text = async () => (await safeText(page)).replace(/\s+/g, " ").slice(0, 300);

await step("D1-project-pages", async () => {
  await goto(`${WEB}/hrms/projects/${projectId}/pages`);
  log.log("TEXT=" + (await text()));
  await shot(page, "20-project-pages", "项目 → Pages");
});

await step("D2-create-page", async () => {
  await page.getByText("Add page", { exact: false }).first().click();
  await page.waitForTimeout(3000);
  await settle(page, 1200, 12000);
  await shot(page, "21-page-composer", "新建页面编辑器");

  // Typing into the page editor is best-effort: it is a rich-text surface and
  // its actionability can be flaky. Every interaction gets a short timeout so a
  // stuck editor cannot stall the whole run.
  const editable = page.locator('[contenteditable="true"]').first();
  const count = await editable.count();
  log.log(`contenteditable count(first)=${count}`);
  if (count) {
    try {
      await editable.click({ timeout: 8000 });
      log.log("clicked title");
      // Bound the keystrokes: the editor autosaves on every input, and an
      // unbounded `keyboard.type` has no timeout of its own.
      await Promise.race([
        page.keyboard.type(PAGE_TITLE, { delay: 10 }),
        new Promise((r) => setTimeout(r, 20000)),
      ]);
      log.log("typed title");
    } catch (e) {
      log.log("title edit skipped: " + e.message.split("\n")[0]);
    }
  }

  // The editor autosaves through PATCH .../pages/<id>/description/; give that
  // request time to land, screenshot, then discard the busy renderer.
  await page.waitForTimeout(4000);
  await shot(page, "22-page-created", "页面已创建（标题已自动保存）");
  log.log("TEXT=" + (await text()));
  await resetPage(page);
});

await step("D3-project-settings", async () => {
  await goto(`${WEB}/hrms/settings/projects/${projectId}/`);
  log.log("url=" + page.url());
  log.log("TEXT=" + (await text()));
  await shot(page, "23-project-settings", "项目设置（General）");
});

await step("D4-workspace-settings", async () => {
  await goto(`${WEB}/hrms/settings/`);
  log.log("TEXT=" + (await text()));
  await shot(page, "24-workspace-settings", "工作区设置（General）");
});

await step(
  "D5-your-work",
  async () => {
    // `Your work` is `/hrms/profile/<userId>/` — the bare `/profile/` is a 404 in
    // Plane itself. Read the real href off the sidebar link instead of guessing.
    //
    // The link only exists in the *workspace* sidebar: `/hrms/settings/` swaps in
    // the settings sidebar, which has no `Your work` entry. Go back to the
    // workspace home first.
    await goto(`${WEB}/hrms/`);
    const href = await page.evaluate(() => {
      const link = [...document.querySelectorAll("a")].find((a) => (a.textContent || "").trim() === "Your work");
      return link ? link.getAttribute("href") : null;
    });
    log.log("your-work href=" + href);
    if (!href) throw new Error("could not find the 'Your work' sidebar link");

    await page.goto(`${WEB}${href}`, { waitUntil: "domcontentloaded" });

    // The profile dashboard is client-rendered and needs three API calls before
    // it has anything to show. A fixed delay is not enough: the SPA paints a
    // spinner first, and a screenshot taken then is a blank page that passes the
    // "no runtime error" check only because nothing rendered yet.
    //
    // This step is also where the ServeMux subtree bug used to show up: the
    // three `user-profile`/`user-stats`/`user-activity` calls returned a
    // *workspace* object with HTTP 200, and the page died in the error boundary.
    await waitForShell(page, 60000);
    await page
      .waitForFunction(
        () => {
          const t = document.body.innerText || "";
          return t.length > 400 && !/Runtime Error|Cannot read properties of undefined/i.test(t);
        },
        null,
        { timeout: 60000 }
      )
      .catch(() => {});
    await waitNoSpinner(page, 30000);
    await settle(page, 1500, 20000);

    log.log("url=" + page.url());
    log.log("TEXT=" + (await text()));
    await shot(page, "25-your-work", "Your work（个人工作台）");
  },
  // Longer than the default 90s: this step now genuinely waits for the profile
  // dashboard to finish loading instead of screenshotting its spinner.
  180000
);

writeNet(net, "run4-project-pages");
log.log("\nFINAL_URL=" + page.url());
await ctx.close();
