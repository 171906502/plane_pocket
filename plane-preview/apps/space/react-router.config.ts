import type { Config } from "@react-router/dev/config";
import { joinUrlPath } from "@plane/utils";

const basePath = joinUrlPath(process.env.VITE_SPACE_BASE_PATH ?? "", "/") ?? "/";

// SPA, so the whole app can be served as static files by the Go binary (or any
// static host) with no Node process in production.
//
// This is not a free switch: react-router's SPA mode rejects two server-only
// route exports, both of which had to be removed (2026-10-18):
//   - `headers` in app/root.tsx and app/page.tsx  -> moved to the static host
//     (see internal/router/static.go `spaceSecurityHeaders`)
//   - `loader` in app/issues/[anchor]/layout.tsx  -> became `clientLoader`
//
// Trade-off: crawlers get an empty HTML shell, so link previews of shared
// issues no longer carry server-rendered OG tags. Humans are unaffected --
// the metadata is still fetched and applied after hydration.
export default {
  appDirectory: "app",
  basename: basePath,
  future: {
    // Without this Vite's dep scanner has no entries, so deps behind route chunks
    // are discovered mid-session, forcing a re-optimization + full page reload.
    unstable_optimizeDeps: true,
  },
  ssr: false,
} satisfies Config;
