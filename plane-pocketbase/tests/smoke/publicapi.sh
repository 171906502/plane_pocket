#!/usr/bin/env bash
# Smoke test for the open API v1 domain (port 8106).
set -u
BASE="http://127.0.0.1:8106"
JAR=".v1-jar.txt"

say() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; exit 1; }
CURL() { curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" "$@"; }

TS=$(date +%s)

say "0. seed: app user, workspace, project, issue"
curl -s -m 15 --noproxy '*' -c "$JAR" "$BASE/auth/get-csrf-token/" -o NUL
CSRF=$(grep csrftoken "$JAR" | awk '{print $7}')
curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" -o NUL -X POST "$BASE/auth/sign-up/" \
  --data-urlencode "email=v1$TS@example.com" \
  --data-urlencode "password=SmokeTest123!" \
  --data-urlencode "csrfmiddlewaretoken=$CSRF" -H "X-CSRFTOKEN: $CSRF"
CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d "{\"name\":\"V1 WS\",\"slug\":\"v1-$TS\",\"organization_size\":\"1-10\"}" \
  "$BASE/api/workspaces/" -o NUL -w "workspace HTTP %{http_code}\n"
PID=$(CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"name":"V1 Proj","identifier":"V1P"}' \
  "$BASE/api/workspaces/v1-$TS/projects/" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
[ -n "$PID" ] || fail "project"
IID=$(CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"name":"V1 work item"}'  \
  "$BASE/api/workspaces/v1-$TS/projects/$PID/issues/" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
echo "project=$PID issue=$IID"

say "1. create an API token directly in the DB-backed collection via app API"
# The app API exposes api-token CRUD under /api/users/api-tokens/ if
# implemented; otherwise insert via a tiny Go-free path is unavailable, so we
# check the token endpoints exist first.
TOKEN=$(curl -s -m 10 --noproxy '*' -b "$JAR" -X POST "$BASE/api/users/api-tokens/" \
  -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" -d '{"label":"v1-test"}' )
echo "$TOKEN" | head -c 200; echo
APIKEY=$(echo "$TOKEN" | sed -E 's/.*"token"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
if [ -z "$APIKEY" ]; then
  echo "(no token endpoint; inserting via debug seed endpoint)"
  APIKEY=$(curl -s -m 10 --noproxy '*' -b "$JAR" -H "X-CSRFTOKEN: $CSRF" \
    "$BASE/api/plane-meta/health/" >/dev/null; echo "")
  fail "cannot obtain API token for testing"
fi

AUTH=(-H "X-Api-Key: $APIKEY")

say "2. GET /api/v1/users/me/"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/users/me/" | head -c 200; echo

say "3. GET /api/v1/workspaces/v1-$TS/projects/"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/workspaces/v1-$TS/projects/" | head -c 200; echo

say "4. GET work-items list (paginated envelope)"
ENV=$(curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/work-items/")
echo "$ENV" | grep -q '"next_cursor"' && echo "$ENV" | grep -q '"results"' || { echo "$ENV" | head -c 200; fail "envelope"; }
echo "envelope OK"

say "5. GET work-item by UUID and by identifier"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/work-items/$IID/" | grep -q '"sequence_id":1' || fail "by uuid"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/workspaces/v1-$TS/work-items/V1P-1/" | grep -q '"sequence_id":1' || fail "by identifier"
echo "both OK"

say "6. POST work item, comment, link via API"
NEW=$(curl -s -m 10 --noproxy '*' "${AUTH[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"name":"API created item","priority":"high"}'  \
  "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/work-items/")
echo "$NEW" | grep -q '"sequence_id":2' || { echo "$NEW" | head -c 200; fail "sequence"; }
NID=$(echo "$NEW" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
curl -s -m 10 --noproxy '*' "${AUTH[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"comment_html":"<p>via api</p>"}'  \
  "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/work-items/$NID/comments/" | grep -q '"comment_html"' || fail "comment"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","title":"ex"}'  \
  "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/work-items/$NID/links/" | grep -q '"url"' || fail "link"
echo "created OK"

say "7. cycles + module via API"
CYC=$(curl -s -m 10 --noproxy '*' "${AUTH[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"name":"Sprint 1"}'  "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/cycles/")
echo "$CYC" | head -c 250; echo
echo "$CYC" | grep -q '"name":"Sprint 1"' || fail "cycle"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"name":"Module A"}'  "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/modules/" | grep -q '"name":"Module A"' || fail "module"
curl -s -m 10 --noproxy '*' "${AUTH[@]}" "$BASE/api/v1/workspaces/v1-$TS/projects/$PID/summary/" | head -c 150; echo

say "8. unauthorized without token"
CODE=$(curl -s -m 10 --noproxy '*' -o NUL -w "%{http_code}" "$BASE/api/v1/users/me/")
[ "$CODE" = "401" ] || fail "expected 401, got $CODE"
echo "401 OK"

printf '\n*** PUBLICAPI SMOKE DONE ***\n'
