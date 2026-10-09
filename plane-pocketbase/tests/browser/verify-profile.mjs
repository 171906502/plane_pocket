// Contract regression suite for the workspace boot + user-profile surface.
//
// Two defect classes are guarded here:
//
// (1) ServeMux subtree patterns (defect #17). Before every route was anchored
//     with `{$}`, `/api/workspaces/{slug}/` was a *subtree* pattern: it matched
//     every unregistered path beneath it and answered with the **workspace
//     object** plus HTTP 200. So:
//       - `/user-profile/{id}/` returned a workspace, and the profile page died
//         on `userProfile.state_distribution.find(...)`;
//       - `/workspace-members/me/`, `/states/`, `/user-properties/` and
//         `/sidebar-preferences/` *appeared* to work while never being
//         implemented at all.
//     The "unmatched sub-paths must 404" section below is the regression guard.
//
// (2) The endpoints those accidental 200s were hiding. They are now implemented
//     and asserted here by shape.
//
// Usage: bash run-one.sh verify-profile.mjs
import { launch, safeText, shot, settle, mkLogger, writeNet, waitForShell, waitNoSpinner, WEB } from "./lib.mjs";

const API = "http://localhost:8000";
const SLUG = "hrms";
const log = mkLogger("verify-profile");

let failures = 0;
function check(name, ok, detail) {
  log.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
  if (!ok) failures++;
}

const { ctx, page, net } = await launch();

/** Fetch an API path from inside the page, so the session cookie is included. */
async function probe(path) {
  // `page.evaluate` has no timeout: if the renderer is busy (the rich-text
  // editor keeps it that way) the call never resolves and the whole script
  // wedges. Race it so a stuck renderer degrades into a reported failure.
  return Promise.race([
    page.evaluate(
      async ([url]) => {
        const res = await fetch(url, { credentials: "include" });
        const text = await res.text();
        let json = null;
        try {
          json = JSON.parse(text);
        } catch {
          /* not json */
        }
        return {
          status: res.status,
          ctype: res.headers.get("content-type") || "",
          json,
          head: text.slice(0, 200),
        };
      },
      [API + path]
    ),
    new Promise((resolve) => setTimeout(() => resolve({ status: 0, json: null, head: "PROBE TIMED OUT" }), 15000)),
  ]);
}

/**
 * Issue a mutating request from inside the page (so the session cookie is
 * included), mirroring what the frontend's API client sends: JSON body plus the
 * `X-CSRFToken` header taken from the `csrftoken` cookie when one exists.
 */
async function patch(path, body) {
  return Promise.race([
    page.evaluate(
      async ([url, payload]) => {
        const csrf = document.cookie
          .split("; ")
          .find((c) => c.startsWith("csrftoken="))
          ?.split("=")[1];
        const res = await fetch(url, {
          method: "PATCH",
          credentials: "include",
          headers: {
            "Content-Type": "application/json",
            ...(csrf ? { "X-CSRFToken": csrf } : {}),
          },
          body: JSON.stringify(payload),
        });
        const text = await res.text();
        let json = null;
        try {
          json = JSON.parse(text);
        } catch {
          /* not json */
        }
        return { status: res.status, ctype: res.headers.get("content-type") || "", json, head: text.slice(0, 200) };
      },
      [API + path, body]
    ),
    new Promise((resolve) => setTimeout(() => resolve({ status: 0, json: null, head: "PATCH TIMED OUT" }), 15000)),
  ]);
}

// Establish the session on the web origin (the persistent profile is already
// signed in; this just makes sure the cookie is in scope).
log.log("== session ==");
await page.goto(`${WEB}/${SLUG}/`, { waitUntil: "domcontentloaded" });
await settle(page, 800, 20000);

const me = await probe("/api/users/me/");
const userId = me.json?.id;
log.log(`  /api/users/me/ -> ${me.status}  id=${userId}`);
check("session established", me.status === 200 && !!userId, `status=${me.status}`);
if (!userId) {
  log.log("cannot continue without a user id");
  await ctx.close();
  process.exit(1);
}

const projects = await probe(`/api/workspaces/${SLUG}/projects/`);
const projectId = Array.isArray(projects.json) ? projects.json[0]?.id : null;
log.log(`  /api/workspaces/${SLUG}/projects/ -> ${projects.status}  first id=${projectId}`);

// ---------------------------------------------------------------- shapes
log.log("");
log.log("== user-profile endpoints ==");

const profile = await probe(`/api/workspaces/${SLUG}/user-profile/${userId}/`);
log.log(`  user-profile -> ${profile.status} ${profile.head}`);
check("user-profile 200", profile.status === 200, `status=${profile.status}`);
check(
  "user-profile.project_data is an array",
  Array.isArray(profile.json?.project_data),
  `got ${JSON.stringify(profile.json?.project_data)?.slice(0, 80)}`
);
check("user-profile.user_data present", !!profile.json?.user_data, JSON.stringify(profile.json?.user_data)?.slice(0, 120));
check(
  "user-profile.user_data has the Django keys",
  ["email", "first_name", "last_name", "avatar_url", "cover_image_url", "date_joined", "user_timezone", "display_name"].every(
    (k) => k in (profile.json?.user_data ?? {})
  )
);

const stats = await probe(`/api/workspaces/${SLUG}/user-stats/${userId}/`);
log.log(`  user-stats -> ${stats.status} ${stats.head}`);
check("user-stats 200", stats.status === 200, `status=${stats.status}`);
check(
  "user-stats.state_distribution is an array",
  Array.isArray(stats.json?.state_distribution),
  JSON.stringify(stats.json?.state_distribution)
);
check("user-stats.priority_distribution is an array", Array.isArray(stats.json?.priority_distribution));
check(
  "user-stats numeric counters",
  ["created_issues", "assigned_issues", "completed_issues", "pending_issues", "subscribed_issues"].every(
    (k) => typeof stats.json?.[k] === "number"
  ),
  JSON.stringify({
    created: stats.json?.created_issues,
    assigned: stats.json?.assigned_issues,
    completed: stats.json?.completed_issues,
    pending: stats.json?.pending_issues,
  })
);
check("user-stats.present_cycles is an array", Array.isArray(stats.json?.present_cycles));
check("user-stats.upcoming_cycles is an array", Array.isArray(stats.json?.upcoming_cycles));

const activity = await probe(`/api/workspaces/${SLUG}/user-activity/${userId}/?per_page=10`);
log.log(`  user-activity -> ${activity.status} ${activity.head}`);
check("user-activity 200", activity.status === 200, `status=${activity.status}`);
check("user-activity.results is an array", Array.isArray(activity.json?.results));
check(
  "user-activity cursor envelope",
  ["count", "total_pages", "total_results", "next_cursor", "next_page_results"].every((k) => k in (activity.json ?? {})),
  `count=${activity.json?.count} total=${activity.json?.total_results}`
);

// ---------------------------------------------------------------- boot surface
// These four endpoints are fetched on *every* workspace load. All four were
// missing, and all four looked healthy because the subtree pattern answered them
// with the workspace object. `/workspace-members/me/` is the strictest of them:
// `layouts/auth-layout/workspace-wrapper.tsx` treats a missing entry as "not a
// member" and renders a full-screen "Not Authorized!" gate over the whole app.
log.log("");
log.log("== workspace boot surface ==");

const memberMe = await probe(`/api/workspaces/${SLUG}/workspace-members/me/`);
log.log(`  workspace-members/me -> ${memberMe.status} ${memberMe.head.slice(0, 160)}`);
check("workspace-members/me 200", memberMe.status === 200, `status=${memberMe.status}`);
check(
  "workspace-members/me carries the membership fields",
  typeof memberMe.json?.role === "number" &&
    !!memberMe.json?.view_props &&
    typeof memberMe.json?.draft_issue_count === "number",
  JSON.stringify({
    role: memberMe.json?.role,
    has_view_props: !!memberMe.json?.view_props,
    draft_issue_count: memberMe.json?.draft_issue_count,
  })
);

const states = await probe(`/api/workspaces/${SLUG}/states/`);
log.log(`  states -> ${states.status} ${states.head.slice(0, 160)}`);
check("workspace states 200 + array", states.status === 200 && Array.isArray(states.json), `status=${states.status}`);
check(
  "workspace states carry a computed `order` and no Triage",
  Array.isArray(states.json) &&
    states.json.length > 0 &&
    states.json.every((s) => typeof s.order === "number" && s.group !== "triage"),
  JSON.stringify((states.json ?? []).map((s) => `${s.group}:${s.order}`))
);

const props = await probe(`/api/workspaces/${SLUG}/user-properties/`);
log.log(`  user-properties -> ${props.status} ${props.head.slice(0, 160)}`);
check("user-properties 200", props.status === 200, `status=${props.status}`);
check(
  "user-properties carries the Django defaults",
  !!props.json?.filters && !!props.json?.display_filters && props.json?.navigation_project_limit === 10,
  JSON.stringify({
    filters: !!props.json?.filters,
    display_filters: !!props.json?.display_filters,
    navigation_project_limit: props.json?.navigation_project_limit,
  })
);

const sidebar = await probe(`/api/workspaces/${SLUG}/sidebar-preferences/`);
log.log(`  sidebar-preferences -> ${sidebar.status} ${sidebar.head.slice(0, 200)}`);
check("sidebar-preferences 200", sidebar.status === 200, `status=${sidebar.status}`);
// Django returns a MAP keyed by preference key, not a list, and seeds all seven
// keys on read.
const sidebarKeys = sidebar.json && typeof sidebar.json === "object" ? Object.keys(sidebar.json) : [];
check(
  "sidebar-preferences is a map with all seven seeded keys",
  ["views", "active_cycles", "analytics", "drafts", "your_work", "archives", "stickies"].every((k) =>
    sidebarKeys.includes(k)
  ),
  sidebarKeys.join(",")
);
check(
  "sidebar-preferences pins drafts / your_work / stickies",
  sidebar.json?.drafts?.is_pinned === true &&
    sidebar.json?.your_work?.is_pinned === true &&
    sidebar.json?.stickies?.is_pinned === true &&
    sidebar.json?.views?.is_pinned === false,
  JSON.stringify({
    drafts: sidebar.json?.drafts?.is_pinned,
    your_work: sidebar.json?.your_work?.is_pinned,
    stickies: sidebar.json?.stickies?.is_pinned,
    views: sidebar.json?.views?.is_pinned,
  })
);

// The workspace home page's widgets. Django's view returns a LIST here (unlike
// the sidebar's map) and seeds the three non-tutorial keys on read.
const widgets = await probe(`/api/workspaces/${SLUG}/home-preferences/`);
log.log(`  home-preferences -> ${widgets.status} ${widgets.head.slice(0, 200)}`);
check(
  "home-preferences 200 + array",
  widgets.status === 200 && Array.isArray(widgets.json),
  `status=${widgets.status} isArray=${Array.isArray(widgets.json)}`
);
check(
  "home-preferences seeds quick_links / recents / my_stickies",
  Array.isArray(widgets.json) &&
    ["quick_links", "recents", "my_stickies"].every((k) =>
      widgets.json.some((w) => w.key === k && typeof w.sort_order === "number" && !!w.config)
    ),
  JSON.stringify((widgets.json ?? []).map((w) => `${w.key}:${w.sort_order}`))
);
check(
  "home-preferences excludes the tutorial widgets",
  Array.isArray(widgets.json) &&
    !widgets.json.some((w) => w.key === "quick_tutorial" || w.key === "new_at_plane"),
  JSON.stringify((widgets.json ?? []).map((w) => w.key))
);
// PATCH carries the key in the path and returns {key, is_enabled, sort_order}
// — deliberately *without* `config`.
const widgetPatch = await patch(`/api/workspaces/${SLUG}/home-preferences/recents/`, { is_enabled: false });
log.log(`  PATCH home-preferences/recents -> ${widgetPatch.status} ${widgetPatch.head.slice(0, 160)}`);
check(
  "PATCH home-preferences/{key} returns key + is_enabled + sort_order (no config)",
  widgetPatch.status === 200 &&
    widgetPatch.json?.key === "recents" &&
    widgetPatch.json?.is_enabled === false &&
    typeof widgetPatch.json?.sort_order === "number" &&
    !("config" in (widgetPatch.json ?? {})),
  JSON.stringify(widgetPatch.json)
);
// Put it back, so a later screenshot run doesn't show a disabled widget.
const widgetRestore = await patch(`/api/workspaces/${SLUG}/home-preferences/recents/`, { is_enabled: true });
check("home-preferences PATCH is reversible", widgetRestore.status === 200 && widgetRestore.json?.is_enabled === true,
  JSON.stringify(widgetRestore.json));

// ---------------------------------------------------------------- 404s
// These are the regression guards for the subtree-pattern bug: every path below
// used to answer 200 with a workspace/project object.
log.log("");
log.log("== unmatched sub-paths must 404 (was: 200 + wrong object) ==");

for (const path of [
  `/api/workspaces/${SLUG}/definitely-not-a-route/`,
  `/api/workspaces/${SLUG}/user-profile/`,
  `/api/workspaces/${SLUG}/user-stats/`,
]) {
  const r = await probe(path);
  check(`404 ${path}`, r.status === 404, `status=${r.status} body=${r.head.slice(0, 60)}`);
}

if (projectId) {
  for (const path of [
    `/api/workspaces/${SLUG}/projects/${projectId}/definitely-not-a-route/`,
    `/api/workspaces/${SLUG}/projects/${projectId}/user-profile/`,
  ]) {
    const r = await probe(path);
    check(`404 ${path}`, r.status === 404, `status=${r.status} body=${r.head.slice(0, 60)}`);
  }
}

// ---------------------------------------------------------------- still-200s
log.log("");
log.log("== the routes that must keep working ==");
for (const [path, want] of [
  [`/api/workspaces/${SLUG}/`, 200],
  [`/api/workspaces/${SLUG}/members/`, 200],
  [`/api/workspaces/${SLUG}/projects/`, 200],
  ...(projectId ? [[`/api/workspaces/${SLUG}/projects/${projectId}/`, 200]] : []),
  [`/api/workspaces/`, 200],
  [`/api/workspaces/${SLUG}/states/`, 200],
  [`/api/workspaces/${SLUG}/user-properties/`, 200],
  [`/api/workspaces/${SLUG}/sidebar-preferences/`, 200],
  [`/api/workspaces/${SLUG}/workspace-members/me/`, 200],
]) {
  const r = await probe(path);
  check(`${want} ${path}`, r.status === want, `status=${r.status}`);
}

// ---------------------------------------------------------------- the page
log.log("");
log.log("== profile page renders ==");

// The SPA paints a spinner first; a screenshot taken then proves nothing (it
// passed the "no runtime error" check simply because nothing had rendered yet).
// Wait for the app shell, then for the spinner to clear, then for the profile
// data itself — the user's display name comes from the user-profile response,
// so its appearance proves the page consumed the endpoint we just fixed.
const displayName = profile.json?.user_data?.display_name ?? "";

await page.goto(`${WEB}/${SLUG}/profile/${userId}/`, { waitUntil: "domcontentloaded" });
await waitForShell(page, 60000);
if (displayName) {
  await page
    .waitForFunction((name) => (document.body.innerText || "").includes(name), displayName, { timeout: 45000 })
    .catch(() => {});
}
await waitNoSpinner(page, 30000);
await settle(page, 1000, 15000);

const text = await safeText(page);
const crashed = /Runtime Error|Cannot read properties of undefined/i.test(text);
check("no runtime error on the profile page", !crashed, text.slice(0, 200).replace(/\s+/g, " "));
check(
  `profile page shows the display name "${displayName}"`,
  !!displayName && text.includes(displayName),
  text.slice(0, 200).replace(/\s+/g, " ")
);
check(
  "profile sidebar lists the project the user is a member of",
  Array.isArray(profile?.json?.project_data) &&
      profile.json.project_data.length > 0,
  JSON.stringify({
    project_data: profile?.json?.project_data?.map((p) => p.name),
    shape_ok: !!profile?.json?.user_data,
  })
);
await shot(page, "verify-profile-page", "个人资料页 — 修复后");

// Also confirm the nested routes the sidebar drives.
await page.goto(`${WEB}/${SLUG}/profile/${userId}/assigned/`, { waitUntil: "domcontentloaded" });
await waitForShell(page, 60000);
await waitNoSpinner(page, 30000);
await settle(page, 1000, 15000);
const assignedText = await safeText(page);

// The assigned tab is driven by GET /api/workspaces/{slug}/user-issues/{userId}/.
//
// The assertion here used to be `assignedText.length > 200` — which passes on a
// COMPLETELY EMPTY list. That is precisely the "the frontend renders a failure as
// a normal-looking state" trap (docs/06-PITFALLS.md D-1): the page looked fine
// while the list was blank, and the missing endpoint stayed invisible.
// So assert the DATA, not the pixel count — and then assert the data reached the DOM.
// Probe with the SAME query the frontend actually sends. The assigned tab
// filters by `assignees=<userId>` (verified from the recorded network log:
// `user-issues/<id>/?order_by=sort_order&sub_issue=false&...&assignees=<id>`).
// Probing the bare path instead returns creator/subscriber matches too, so the
// check would pass while the rendered tab was still empty — a false green.
const assignedApi = await probe(
  `/api/workspaces/${SLUG}/user-issues/${userId}/?assignees=${userId}&sub_issue=false&per_page=100`
);
check(
  "assigned tab: /user-issues/ API returns 200",
  assignedApi.status === 200,
  `status=${assignedApi.status} head=${String(assignedApi.head).slice(0, 120)}`
);

const assignedTotal = assignedApi.json?.total_count ?? 0;
check(
  "assigned tab: /user-issues/ reports at least one issue",
  assignedTotal > 0,
  `total_count=${assignedTotal}`
);

const assignedResults = Array.isArray(assignedApi.json?.results) ? assignedApi.json.results : [];
check(
  "assigned tab: results array is populated",
  assignedResults.length > 0,
  `results.length=${assignedResults.length}`
);

check(
  "assigned tab renders without a runtime error",
  !/Runtime Error|Cannot read properties of undefined/i.test(assignedText),
  assignedText.slice(0, 160).replace(/\s+/g, " ")
);

// The strongest available check: the issue title returned by the API must
// actually appear on the rendered page. This is what "the list is no longer
// empty" means in practice, and it cannot pass on a blank list.
const firstIssueName = assignedResults[0]?.name ?? "";
check(
  "assigned tab: the returned issue title is actually rendered",
  firstIssueName.length > 0 && assignedText.includes(firstIssueName),
  firstIssueName
    ? `looking for "${firstIssueName}"`
    : "API returned no issue name to look for"
);

await shot(page, "verify-profile-assigned", "个人资料页 — assigned 标签");

writeNet(net, "verify-profile");

log.log("");
log.log(failures === 0 ? "RESULT: ALL CHECKS PASSED" : `RESULT: ${failures} CHECK(S) FAILED`);

await ctx.close();
process.exit(failures === 0 ? 0 : 1);
