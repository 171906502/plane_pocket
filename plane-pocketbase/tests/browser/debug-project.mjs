import { launch, WEB, shot, settle, waitForShell, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();

const PROJECT = "5d8df2bb-de18-4986-91d3-47e4f3613b7e";
const seen = [];

page.on("response", async (res) => {
  const url = res.url();
  if (!url.includes("/api/")) return;
  let body = "";
  try {
    body = (await res.text()).slice(0, 300);
  } catch {
    body = "<no body>";
  }
  seen.push(`${res.status()} ${res.request().method()} ${url.replace("http://localhost:8000", "")}\n     ${body}`);
});

await page.goto(`${WEB}/hrms/projects/${PROJECT}/issues/`, { waitUntil: "domcontentloaded" });
await waitForShell(page);
await settle(page, 2000, 25000);

// Ask the API from inside the page, with the page's own cookies.
const direct = await page.evaluate(async () => {
  const r = await fetch("http://localhost:8000/api/users/me/workspaces/hrms/project-roles/", {
    credentials: "include",
  });
  return { status: r.status, body: await r.text() };
});
log.log("DIRECT project-roles: " + JSON.stringify(direct));

const meInfo = await page.evaluate(async () => {
  const r = await fetch("http://localhost:8000/api/workspaces/hrms/workspace-members/me/", { credentials: "include" });
  return { status: r.status, body: (await r.text()).slice(0, 300) };
});
log.log("DIRECT workspace-members/me: " + JSON.stringify(meInfo));

log.log("\n--- ALL /api/ RESPONSES ---");
for (const s of seen) log.log(s);

await shot(page, "debug-project-page");
await ctx.close();
