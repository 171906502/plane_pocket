#!/usr/bin/env bash
# Smoke test for the instance-admin + public-space domains (port 8105).
set -u
BASE="http://127.0.0.1:8105"
JAR=".is-jar.txt"

say() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; exit 1; }
CURL() { curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" "$@"; }

say "1. public instance info (anonymous, expect config+instance envelope)"
INFO=$(CURL "$BASE/api/instances/")
echo "$INFO" | head -c 300; echo
echo "$INFO" | grep -q '"instance":{' || fail "instance envelope missing (seed row missing)"

say "2. admin sign-up (form POST, expect 302 + admin-session-id)"
TS=$(date +%s)
curl -s -m 15 --noproxy '*' -c "$JAR" "$BASE/auth/get-csrf-token/" -o NUL
CSRF=$(grep csrftoken "$JAR" | awk '{print $7}')
curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" -o NUL -w "signup HTTP %{http_code}\n" -X POST \
  "$BASE/api/instances/admins/sign-up/" \
  --data-urlencode "email=admin$TS@example.com" \
  --data-urlencode "password=AdminPass123" \
  --data-urlencode "first_name=Ada" \
  --data-urlencode "company_name=ACME" \
  --data-urlencode "csrfmiddlewaretoken=$CSRF" \
  -H "X-CSRFTOKEN: $CSRF"
grep -q admin-session-id "$JAR" || fail "no admin-session-id cookie set"

say "3. session check (expect is_authenticated true)"
SESS=$(CURL "$BASE/api/instances/admins/session/")
echo "$SESS" | head -c 200; echo
echo "$SESS" | grep -q '"is_authenticated":true' || fail "session not authenticated"

say "4. second sign-up attempt (expect ADMIN_ALREADY_EXIST redirect)"
curl -s -m 15 --noproxy '*' -o NUL -w "HTTP %{http_code} -> %{redirect_url}\n" -X POST \
  "$BASE/api/instances/admins/sign-up/" \
  --data-urlencode "email=x$TS@example.com" \
  --data-urlencode "password=AdminPass123" \
  --data-urlencode "first_name=X" \
  --data-urlencode "csrfmiddlewaretoken=$CSRF" -H "X-CSRFTOKEN: $CSRF" | tee /dev/null | grep -q "302" || fail "expected 302"

say "5. admin configurations (expect 200 list)"
CURL -o NUL -w "configurations HTTP %{http_code}\n" "$BASE/api/instances/configurations/"

say "6. workspace-slug-check (expect status true for free slug)"
CURL "$BASE/api/instances/workspace-slug-check/?slug=freeslug$TS" | head -c 100; echo

say "7. workspace + project as regular app user"
curl -s -m 15 --noproxy '*' -c "$JAR" "$BASE/auth/get-csrf-token/" -o NUL
CSRF=$(grep csrftoken "$JAR" | awk '{print $7}')
curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" -o NUL -X POST "$BASE/auth/sign-up/" \
  --data-urlencode "email=user$TS@example.com" \
  --data-urlencode "password=SmokeTest123!" \
  --data-urlencode "csrfmiddlewaretoken=$CSRF" -H "X-CSRFTOKEN: $CSRF"
CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d "{\"name\":\"WS $TS\",\"slug\":\"ws-$TS\",\"organization_size\":\"1-10\"}" \
  "$BASE/api/workspaces/" -o NUL -w "workspace HTTP %{http_code}\n"
PROJ=$(CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"name":"Pub Project","identifier":"PUB","network":2}' \
  "$BASE/api/workspaces/ws-$TS/projects/")
PID=$(echo "$PROJ" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
echo "project=$PID"
[ -n "$PID" ] || fail "project not created"

say "8. publish the project (POST project-deploy-boards)"
BOARD=$(CURL -X POST -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" -d '{}' \
  "$BASE/api/workspaces/ws-$TS/projects/$PID/project-deploy-boards/")
ANCHOR=$(echo "$BOARD" | sed -E 's/.*"anchor"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
echo "anchor=$ANCHOR"
[ -n "$ANCHOR" ] || fail "deploy board not created"
echo "$BOARD" | head -c 300; echo

say "9. public space: meta + settings + states (anonymous)"
curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/meta/" | head -c 250; echo
curl -s -m 15 --noproxy '*' -o NUL -w "settings HTTP %{http_code}\n" "$BASE/api/public/anchor/$ANCHOR/settings/"
curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/states/" | head -c 200; echo

say "10. public space: issues list (expect empty envelope)"
CURL "$BASE/api/public/anchor/$ANCHOR/issues/" | head -c 150; echo

say "11. anonymous anchor for unpublished project (expect 404)"
curl -s -m 15 --noproxy '*' -o NUL -w "unpublished anchor HTTP %{http_code}\n" \
  "$BASE/api/public/workspaces/ws-$TS/projects/00000000-0000-0000-0000-000000000000/anchor/"

printf '\n*** INSTANCE/SPACE SMOKE DONE ***\n'
