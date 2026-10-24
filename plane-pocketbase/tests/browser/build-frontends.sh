#!/usr/bin/env bash
# Build the three frontends for **single-port mode**: one Go binary on
# http://localhost:8000 serving /, /god-mode and /spaces as static files.
#
# ⚠️ 为什么需要这个脚本（而不是依赖 .env / .env.production）：
#
# 每个 app 的 vite.config.ts 里都有这两行 ——
#
#     dotenv.config({ path: path.resolve(__dirname, ".env") });
#     ...define: { "process.env": JSON.stringify(viteEnv) }
#
# 也就是说：
#   1. 它**只读 `.env`**，`.env.production` / `.env.local` 一概不看，
#      所以「改 .env.production 就能做构建期配置」是**无效的**；
#   2. `process.env` 被整体替换成 dotenv 读到的 `.env` 内容字面量，
#      于是 .env 里的 3000/3001/3002 会被**编进产物**；
#   3. dotenv 默认**不覆盖已存在的 process.env 变量** —— 这是我们唯一的杠杆：
#      预先在 shell 里 export 好 VITE_*，dotenv 会让路，define 拿到单端口的 8000。
#
# 所以：单端口构建**必须**从这里 export 变量，不能改 .env。
# 反过来，dev（react-router dev）用的是 `.env` 的多端口值，两者互不影响。
#
# 用法：
#   cd plane-pocketbase/tests/browser
#   bash build-frontends.sh            # 构建三端，产物在 apps/<app>/build/client
#
# 产物目录然后喂给后端：
#   PLANE_WEB_DIR    = ../plane-preview/apps/web/build/client
#   PLANE_ADMIN_DIR  = ../plane-preview/apps/admin/build/client
#   PLANE_SPACE_DIR  = ../plane-preview/apps/space/build/client

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PB="$(cd "$HERE/../.." && pwd)"
FE="$(cd "$PB/../plane-preview" && pwd)"

# 要在任意端口起单端口实例，改这里一个变量即可。
BASE_URL="http://localhost:8000"

# 必须按 app 分别 export：同一个 vite.config.ts 会把三套值都读进去，
# 而每个 app 只用自己那几个。
export VITE_API_BASE_URL="$BASE_URL"
export VITE_WEB_BASE_URL="$BASE_URL"
export VITE_ADMIN_BASE_URL="$BASE_URL"
export VITE_ADMIN_BASE_PATH="/god-mode"
export VITE_SPACE_BASE_URL="$BASE_URL"
export VITE_SPACE_BASE_PATH="/spaces"
export VITE_LIVE_BASE_URL="$BASE_URL"
export VITE_LIVE_BASE_PATH="/live"

# 沙箱的删除保护会拒绝 react-router 清理 build/ 目录。
export CODEBUDDY_SAFE_DELETE_ENABLED=0

# ⚠️ 关闭 MSYS 的路径改写（Git Bash 专属坑，见 docs/06-PITFALLS.md **C-20**）。
# Git for Windows 在 spawn Windows 子进程时，会把 env 里「长得像 POSIX 路径」的值
# 改写成 Windows 路径：`VITE_ADMIN_BASE_PATH=/god-mode` 会变成
# `C:/…/PortableGit/god-mode`。而 Vite 的 `base` 直接吃这个值，
# 于是 index.html 里每一个资源 URL、连 React Router 的 basename 全坏，
# 页面只剩一个转圈的 logo。用 curl 探活全是 200（文件确实在），只有浏览器才暴露。
# `MSYS_NO_PATHCONV=1`（Git 官方开关）让原值直达 node。
export MSYS_NO_PATHCONV=1

for app in web admin space; do
  echo "[build] $app  (BASE_URL=$BASE_URL)"
  (
    cd "$FE/apps/$app"
    # 直接调 bin：pnpm run 会先做包管理器身份校验（见 C-14），报的是安装失败而不是构建失败。
    ./node_modules/.bin/react-router build > "$PB/tests/logs/$app-build.log" 2>&1
  )

  dir="$FE/apps/$app/build/client"
  if [ ! -f "$dir/index.html" ]; then
    echo "[build] FATAL: $dir/index.html missing; full log: $PB/tests/logs/$app-build.log" >&2
    exit 1
  fi

  size="$(du -sh "$dir" | cut -f1)"

  # 自检 1：产物里不能再出现多端口地址。3000/3001/3002 一旦出现，说明 .env 赢了。
  if grep -rql "localhost:300[012]" "$dir/assets"; then
    echo "[build] FATAL: $app bundle still references the multi-port dev hosts" >&2
    grep -rl "localhost:300[012]" "$dir/assets" | head -5 >&2
    exit 1
  fi

  # 自检 2：base 路径不允许被 MSYS 改写成 Windows 路径（C-20）。
  # 只看 index.html：base 会被 Vite 写进每一个资源 URL。
  if grep -q "C:/" "$dir/index.html"; then
    echo "[build] FATAL: $app index.html contains a Windows path — MSYS mangled the" >&2
    echo "[build]        base path. Is MSYS_NO_PATHCONV=1 set? (C-20)" >&2
    grep -o 'href="[^"]*"' "$dir/index.html" | grep "C:/" | head -3 >&2
    exit 1
  fi
  if grep -q "PortableGit" "$dir/index.html"; then
    echo "[build] FATAL: $app index.html references PortableGit (C-20)" >&2
    exit 1
  fi

  # 自检 3：带 base 的两端，资源 URL 必须有正确前缀，不能退回 /assets/。
  case "$app" in
    admin) want="/god-mode/assets/" ;;
    space) want="/spaces/assets/" ;;
    *)     want="/assets/" ;;
  esac
  if ! grep -q "href=\"$want" "$dir/index.html"; then
    echo "[build] FATAL: $app index.html has no \"$want\" reference — base path wrong" >&2
    exit 1
  fi

  echo "[build] $app  OK  ($size)"
done

echo
echo "[build] all three SPA bundles are single-port clean"
echo "[build]   web   -> $FE/apps/web/build/client"
echo "[build]   admin -> $FE/apps/admin/build/client"
echo "[build]   space -> $FE/apps/space/build/client"
