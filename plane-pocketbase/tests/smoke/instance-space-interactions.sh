#!/usr/bin/env bash
# Phase 2: public interaction flows over an already-seeded server (port 8105).
set -u
BASE="http://127.0.0.1:8105"
JAR=".is-jar2.txt"

say() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; exit 1; }
CURL() { curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" "$@"; }

TS=$(date +%s)

say "1. app session: workspace + project + publish + issue"
curl -s -m 15 --noproxy '*' -c "$JAR" "$BASE/auth/get-csrf-token/" -o NUL
CSRF=$(grep csrftoken "$JAR" | awk '{print $7}')
curl -s -m 15 --noproxy '*' -b "$JAR" -c "$JAR" -o NUL -X POST "$BASE/auth/sign-up/" \
  --data-urlencode "email=pub$TS@example.com" \
  --data-urlencode "password=SmokeTest123!" \
  --data-urlencode "csrfmiddlewaretoken=$CSRF" -H "X-CSRFTOKEN: $CSRF"
CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d "{\"name\":\"WS2 $TS\",\"slug\":\"ws2-$TS\",\"organization_size\":\"1-10\"}" \
  "$BASE/api/workspaces/" -o NUL
PID=$(CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"name":"Board Proj","identifier":"BRD"}' \
  "$BASE/api/workspaces/ws2-$TS/projects/" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
[ -n "$PID" ] || fail "project"
ANCHOR=$(CURL -X POST -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" -d '{}' \
  "$BASE/api/workspaces/ws2-$TS/projects/$PID/project-deploy-boards/" | sed -E 's/.*"anchor"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
[ -n "$ANCHOR" ] || fail "publish"
IID=$(CURL -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"name":"Public work item"}' \
  "$BASE/api/workspaces/ws2-$TS/projects/$PID/issues/" | sed -E 's/.*"id"[[:space:]]*:[[:space:]]*"([0-9a-f-]{36})".*/\1/')
echo "issue=$IID anchor=$ANCHOR"

say "2. anonymous issues list finds the issue"
LIST=$(curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/issues/")
echo "$LIST" | grep -q '"name":"Public work item"' || fail "issue not in public list"
echo "list OK"

say "3. anonymous issue detail with reactions+votes keys"
DET=$(curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/issues/$IID/")
echo "$DET" | grep -q '"reactions":\[' && echo "$DET" | grep -q '"votes":\[' || { echo "$DET" | head -c 300; fail "detail shape"; }
echo "detail OK"

say "4. anonymous comment POST (expect 401)"
CODE=$(curl -s -m 15 --noproxy '*' -o NUL -w "%{http_code}" -X POST \
  "$BASE/api/public/anchor/$ANCHOR/issues/$IID/comments/" -H "Content-Type: application/json" \
  -d '{"comment_html":"<p>hi</p>"}')
[ "$CODE" = "401" ] || fail "expected 401, got $CODE"
echo "401 OK"

say "5. anonymous intake submission (expect 400 intake-not-enabled or 201)"
curl -s -m 15 --noproxy '*' -o NUL -w "intake HTTP %{http_code}\n" -X POST \
  "$BASE/api/public/anchor/$ANCHOR/intakes/none/intake-issues/" \
  -H "Content-Type: application/json" -d '{"issue":{"name":"Inbox item"}}'

say "6. authenticated comment POST + reaction + vote, then anonymous GET"
CURL -X POST -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"comment_html":"<p>first!</p>"}' \
  "$BASE/api/public/anchor/$ANCHOR/issues/$IID/comments/" -o NUL -w "comment HTTP %{http_code}\n"
CURL -X POST -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"reaction":"🎉"}' \
  "$BASE/api/public/anchor/$ANCHOR/issues/$IID/reactions/" -o NUL -w "reaction HTTP %{http_code}\n"
CURL -X POST -H "X-CSRFTOKEN: $CSRF" -H "Content-Type: application/json" \
  -d '{"vote":1}' \
  "$BASE/api/public/anchor/$ANCHOR/issues/$IID/votes/" -o NUL -w "vote HTTP %{http_code}\n"
DET=$(curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/issues/$IID/")
echo "$DET" | grep -q '"comment_html":"<p>first!</p>"' || fail "comment missing in detail"
# JSON is UTF-8 and curl/grep can differ by locale on Windows; parse the
# semantic values instead of depending on a raw emoji byte match.
PYTHON="C:/Users/cao/.workbuddy-ai/binaries/python/versions/3.13.12/python.exe"
printf '%s' "$DET" | "$PYTHON" -c 'import json,sys; d=json.load(sys.stdin); assert any(x.get("reaction")=="🎉" for x in d.get("reactions", [])); assert any(x.get("vote")==1 for x in d.get("votes", []))' || fail "reaction/vote missing in detail"
COMMENTS=$(curl -s -m 15 --noproxy '*' "$BASE/api/public/anchor/$ANCHOR/issues/$IID/comments/")
echo "$COMMENTS" | grep -q 'first!' || fail "public comment list"
echo "interaction flows OK"

printf '\n*** SPACE INTERACTION SMOKE DONE ***\n'
