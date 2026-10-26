#!/usr/bin/env node
// Build the `packages/*` workspace libraries WITHOUT going through a package
// manager. Run from the Plane repo root (`plane-preview/` or a fresh clone):
//
//     node plane-pocketbase/tests/frontend/build-packages.mjs
//
// ── Why not turbo / pnpm -r ────────────────────────────────────────────────
//
// Both end up executing `pnpm run build`, and pnpm >= 10 verifies the dependency
// tree *before every run*, re-installing the whole workspace when the recorded
// state does not match the current config. On this machine that combination is
// fatal in three separate ways (all measured, see docs/06-PITFALLS.md C-14b):
//
//   1. the project pins `pnpm@11.10.0`; pnpm tries to download it and dies with
//      "resolve the installed @pnpm/exe … os error 2";
//   2. our install uses `--node-linker hoisted` while the default is `isolated`,
//      so every `pnpm run` sees a mismatch and re-installs;
//   3. turbo runs N packages in parallel → N concurrent workspace installs
//      fighting over one lock. Measured: 20+ minutes, zero `dist/`, five idle
//      `pnpm install` children.
//
// The fix is to not ask pnpm to run scripts at all. Every package build here is
// a `tsdown` / `tsc` invocation, and `node_modules/.bin` already contains those
// binaries, so a ~40-line topological runner is both simpler and deterministic.
//
// The only wrinkle is nested `pnpm run <script>` inside a script body
// (`@plane/i18n` has `build: "pnpm run generate:types && tsdown"`); those are
// inlined from the same package.json instead of shelled out.
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

const ROOT = process.cwd();
const BIN = join(ROOT, "node_modules", ".bin");

if (!existsSync(BIN)) {
  console.error("[pkgs] FATAL: node_modules/.bin missing — run install-deps.sh first");
  process.exit(1);
}

// ── collect ────────────────────────────────────────────────────────────────
/** @type {Map<string, {dir: string, json: any}>} */
const packages = new Map();
for (const entry of readdirSync(join(ROOT, "packages"), { withFileTypes: true })) {
  if (!entry.isDirectory()) continue;
  const dir = join(ROOT, "packages", entry.name);
  const pj = join(dir, "package.json");
  if (!existsSync(pj)) continue;
  const json = JSON.parse(readFileSync(pj, "utf8"));
  if (!json.name) continue;
  packages.set(json.name, { dir, json });
}

// ── topological order (a package builds after the workspace packages it uses) ─
const order = [];
const state = new Map(); // 1 = visiting, 2 = done
function visit(name, stack) {
  const s = state.get(name);
  if (s === 2) return;
  if (s === 1) {
    console.error(`[pkgs] FATAL: dependency cycle ${[...stack, name].join(" -> ")}`);
    process.exit(1);
  }
  state.set(name, 1);
  const deps = {
    ...(packages.get(name).json.dependencies ?? {}),
    ...(packages.get(name).json.devDependencies ?? {}),
  };
  for (const dep of Object.keys(deps)) {
    if (packages.has(dep)) visit(dep, [...stack, name]);
  }
  state.set(name, 2);
  order.push(name);
}
for (const name of packages.keys()) visit(name, []);

// ── inline `pnpm run x` / `npm run x` from the same package.json ────────────
function expand(pkg, cmd, depth = 0) {
  if (depth > 5) return cmd;
  return cmd.replace(/\b(?:pnpm|npm)\s+run\s+([\w:.-]+)/g, (whole, name) => {
    const body = pkg.json.scripts?.[name];
    if (!body) return whole; // let it fail loudly rather than silently no-op
    return `(${expand(pkg, body, depth + 1)})`;
  });
}

// ── build ──────────────────────────────────────────────────────────────────
const env = {
  ...process.env,
  // `.bin` first, so `tsdown` / `tsc` resolve to this install. Both separators
  // are set because Windows env keys are case-insensitive but Node keeps the
  // casing it was given.
  PATH: `${BIN};${process.env.PATH ?? ""}`,
  Path: `${BIN};${process.env.PATH ?? ""}`,
  // Belt and braces: if anything below still shells out to pnpm, do not let it
  // try to download the pinned version (C-14b #1).
  npm_config_manage_package_manager_versions: "false",
  // tsdown clears `dist/` before writing. In this sandbox any delete of 50+
  // files is held for approval and, being non-interactive, fails the build —
  // `@plane/editor` hits exactly that (it has ~50 files in `dist/styles`).
  // Same switch build-frontends.sh sets for react-router (C-20 adjacent).
  CODEBUDDY_SAFE_DELETE_ENABLED: "0",
};

let built = 0;
const failures = [];

for (const name of order) {
  const pkg = packages.get(name);
  const script = pkg.json.scripts?.build;
  if (!script) continue;

  const cmd = expand(pkg, script);
  const label = name.replace(/^@plane\//, "");
  const started = Date.now();
  process.stdout.write(`[pkgs] ${label.padEnd(16)} ${cmd}\n`);

  const res = spawnSync(cmd, { cwd: pkg.dir, shell: true, stdio: "inherit", env });
  const secs = ((Date.now() - started) / 1000).toFixed(1);

  if (res.status !== 0) {
    console.error(`[pkgs] ${label} FAILED (${secs}s, exit ${res.status ?? res.signal})`);
    failures.push(label);
  } else {
    console.log(`[pkgs] ${label} ok (${secs}s)`);
    built += 1;
  }
}

// ── assert every package that declares a build script produced dist/ ───────
let missing = 0;
for (const [name, pkg] of packages) {
  if (!pkg.json.scripts?.build) continue;
  if (!existsSync(join(pkg.dir, "dist"))) {
    console.error(`[pkgs] WARN: ${name} produced no dist/`);
    missing += 1;
  }
}

if (failures.length || missing) {
  console.error(
    `[pkgs] FATAL: ${failures.length} build failure(s), ${missing} missing dist/`,
  );
  process.exit(1);
}

console.log(`[pkgs] done — ${built} package(s) built`);
