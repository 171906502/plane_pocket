import { launch, ADMIN, shot, describe, settle, mkLogger } from "./lib.mjs";

const log = mkLogger();
const { ctx, page } = await launch();

await page.goto(ADMIN + "/god-mode/", { waitUntil: "domcontentloaded" });
await settle(page, 1200, 15000);
log.log("URL: " + page.url());
await shot(page, "recon-admin");
await describe(page);

await ctx.close();
