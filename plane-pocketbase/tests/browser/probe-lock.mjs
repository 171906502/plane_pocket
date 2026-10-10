import { chromium } from "playwright-core";
const EDGE = "C://Program Files (x86)//Microsoft//Edge//Application//msedge.exe";
const PROFILE = "D://ai-work//plane_pocket//plane-pocketbase//tests//browser//profile";
try {
  const ctx = await chromium.launchPersistentContext(PROFILE, {
    executablePath: EDGE, headless: true,
    args: ["--no-first-run", "--no-default-browser-check", "--no-proxy-server", "--proxy-bypass-list=*"],
  });
  console.log("LAUNCH_OK pages=" + ctx.pages().length);
  const cookies = await ctx.cookies();
  console.log("COOKIES=" + cookies.filter(c => /session|pb_/.test(c.name)).map(c => c.name + "@" + c.domain).join(","));
  await ctx.close();
  console.log("CLOSED_OK");
} catch (e) {
  console.log("LAUNCH_FAIL: " + e.message.split("\n")[0]);
}
