// Verify the admin instance page works on BOTH hostname forms.
//
// The user's screenshot showed the "We were unable to fetch the details of the
// instance" failure view at http://127.0.0.1:3001/god-mode/. Root cause was a
// CORS origin mismatch: PocketBase was started with --origins listing only
// http://localhost:3001, so a request whose Origin was http://127.0.0.1:3001
// got no Access-Control-Allow-Origin header and the browser rejected the
// credentialed response.
//
// This script drives a real browser to each origin and asserts the page did NOT
// fall into the failure view. It asserts content, not "the page didn't crash".
import { launch, shot, describe, settle, mkLogger } from "./lib.mjs";

const log = mkLogger();
let failures = 0;
const check = (name, ok, detail = "") => {
  console.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  " + detail : ""}`);
  if (!ok) failures++;
};

const ORIGINS = ["http://localhost:3001", "http://127.0.0.1:3001"];

for (const origin of ORIGINS) {
  console.log(`\n=== ${origin}/god-mode/ ===`);
  const { ctx, page } = await launch();

  // Collect the API calls the page makes, and any that failed at the network
  // layer. A CORS rejection shows up here as a request that never completed.
  const apiCalls = [];
  const consoleErrors = [];
  page.on("response", (r) => {
    const u = r.url();
    if (u.includes(":8000/api/")) apiCalls.push(`${r.status()} ${u.replace("http://127.0.0.1:8000", "")}`);
  });
  page.on("requestfailed", (r) => {
    const u = r.url();
    if (u.includes(":8000")) consoleErrors.push(`REQUEST FAILED ${r.failure()?.errorText} ${u}`);
  });
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(`console.error: ${m.text().slice(0, 200)}`);
  });

  await page.goto(origin + "/god-mode/", { waitUntil: "domcontentloaded" });
  await settle(page, 1500, 20000);

  const bodyText = (await page.evaluate(() => document.body.innerText)) || "";
  const hasFailure = bodyText.includes("unable to fetch the details of the instance");
  const hasSignIn = /sign in|continue with|email/i.test(bodyText);

  console.log(`  api calls: ${apiCalls.length ? apiCalls.join(" | ") : "(none)"}`);
  if (consoleErrors.length) console.log(`  errors: ${consoleErrors.slice(0, 3).join(" | ")}`);

  check(`${origin}  instance info fetched (no failure view)`, !hasFailure,
    hasFailure ? "<<< FAILURE VIEW RENDERED" : "");
  check(`${origin}  reached sign-in form`, hasSignIn);
  check(`${origin}  /api/instances/ returned 200`, apiCalls.some((c) => c.startsWith("200 ")));

  await shot(page, `verify-admin-origin-${origin.includes("127") ? "127" : "localhost"}`);
  await ctx.close();
}

console.log(`\n${failures === 0 ? "ALL CHECKS PASSED" : `${failures} CHECK(S) FAILED`}`);
process.exit(failures === 0 ? 0 : 1);
