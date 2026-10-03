import { launch, WEB, shot, describe, settle, mkLogger } from "./lib.mjs";

const log = mkLogger("recon");
const { ctx, page } = await launch();

await page.goto(WEB + "/", { waitUntil: "domcontentloaded" });
await settle(page, 900, 12000);
log.log("URL: " + page.url());
await shot(page, "recon-00-landing");
await describe(page);

await ctx.close();
