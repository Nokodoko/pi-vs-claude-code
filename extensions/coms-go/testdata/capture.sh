#!/usr/bin/env bash
# capture.sh — regenerate golden fixtures from the running TS reference server.
#
# Usage:
#   cd /path/to/pi-vs-claude-code
#   extensions/coms-go/testdata/capture.sh [--port 43210] [--token test-token]
#
# Prerequisites: bun >= 1.3.2, jq
#
# This script must NOT run as part of normal `go test`. It is a one-time
# regeneration tool. Run only when the TS server wire protocol changes.
# Fixtures are checked into git so integration tests do not require Bun.

set -euo pipefail

PORT=${PORT:-43210}
TOKEN=${TOKEN:-test-token}
BASE="http://127.0.0.1:$PORT"
OUT="extensions/coms-go/testdata/golden"

# ──── helpers ────────────────────────────────────────────────────────────────

log() { printf '[capture] %s\n' "$*" >&2; }

canon() {
  # Canonicalize dynamic values: ULIDs, ISO timestamps, SSE URL paths.
  jq --sort-keys '
    walk(
      if type == "string" then
        if test("^[0-9A-HJKMNP-TV-Z]{26}$") then "<ulid>"
        elif test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T") then "<iso>"
        elif test("^/v1/events\\?") then "<sse_url>"
        else . end
      else . end
    )
  '
}

require() {
  command -v "$1" &>/dev/null || { log "missing dependency: $1"; exit 1; }
}

require bun
require jq
require curl

# ──── start TS server ────────────────────────────────────────────────────────

log "starting TS server on port $PORT with token=$TOKEN"
PI_COMS_NET_AUTH_TOKEN="$TOKEN" PI_COMS_NET_PORT="$PORT" \
  bun scripts/coms-net-server.ts > /tmp/coms-capture-ts.log 2>&1 &
TS_PID=$!
log "TS server pid=$TS_PID"
sleep 1

cleanup() {
  log "stopping TS server (pid=$TS_PID)"
  kill "$TS_PID" 2>/dev/null || true
  wait "$TS_PID" 2>/dev/null || true
}
trap cleanup EXIT

# ──── health ─────────────────────────────────────────────────────────────────

log "capturing /health"
curl -sf "$BASE/health" | canon > "$OUT/health.resp.json"

# ──── register (happy path) ───────────────────────────────────────────────────

log "capturing POST /v1/agents/register (happy)"
SID1="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
curl -sf -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"session_id\":\"$SID1\",\"name\":\"planner\",
       \"purpose\":\"Plans the work\",\"model\":\"claude-opus-4-7\",
       \"color\":\"#36F9F6\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  "$BASE/v1/agents/register" | canon > "$OUT/register_happy.resp.json"

# ──── register (no auth → 401) ───────────────────────────────────────────────

log "capturing POST /v1/agents/register (no auth)"
curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"session_id\":\"anon\",\"name\":\"planner\"}" \
  "$BASE/v1/agents/register" | jq '.' > "$OUT/register_no_auth.resp.json"

# ──── list agents (populated) ─────────────────────────────────────────────────

log "capturing GET /v1/agents (populated)"
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents?project=default" | canon > "$OUT/list_agents_populated.resp.json"

# ──── list agents (empty) ─────────────────────────────────────────────────────
# Register on an isolated project so the list is empty.
log "capturing GET /v1/agents (empty)"
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents?project=__empty_project__" | jq '.' > "$OUT/list_agents_empty.resp.json"

# ──── send message (happy, no SSE stream → queued) ────────────────────────────

log "capturing POST /v1/messages (happy, queued)"
SID2="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
curl -sf -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"session_id\":\"$SID2\",\"name\":\"coder\",
       \"purpose\":\"Writes code\",\"model\":\"claude-opus-4-7\",
       \"color\":\"#FF6600\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  "$BASE/v1/agents/register" > /dev/null

SEND_RESP=$(curl -sf -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"sender_session\":\"$SID1\",
       \"target\":\"coder\",\"prompt\":\"Hello coder\",\"hops\":0}" \
  "$BASE/v1/messages")
echo "$SEND_RESP" | canon > "$OUT/send_message_happy.resp.json"
MSG_ID=$(echo "$SEND_RESP" | jq -r '.msg_id')

# ──── get message (queued) ────────────────────────────────────────────────────

log "capturing GET /v1/messages/:id (queued)"
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/messages/$MSG_ID" | canon > "$OUT/get_message_queued.resp.json"

# ──── await (timeout) ─────────────────────────────────────────────────────────

log "capturing GET /v1/messages/:id/await (timeout)"
SID3="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
SID4="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
for pair in "$SID3:awaiter" "$SID4:answerer2"; do
  sid="${pair%%:*}"; nm="${pair#*:}"
  curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"project\":\"await_proj\",\"session_id\":\"$sid\",\"name\":\"$nm\",
         \"purpose\":\"x\",\"model\":\"m\",\"color\":\"#fff\",\"cwd\":\"/tmp\",\"explicit\":false}" \
    "$BASE/v1/agents/register" > /dev/null
done
AWAIT_MSG=$(curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"await_proj\",\"sender_session\":\"$SID3\",\"target\":\"answerer2\",\"prompt\":\"wait\",\"hops\":0}" \
  "$BASE/v1/messages" | jq -r '.msg_id')
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/messages/$AWAIT_MSG/await?timeout_ms=200" | canon > "$OUT/await_timeout.resp.json"

# ──── submit response + await complete ───────────────────────────────────────

log "capturing POST /v1/messages/:id/response (happy)"
curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"responder_session\":\"$SID2\",\"response\":\"done\"}" \
  "$BASE/v1/messages/$MSG_ID/response" | jq '.' > "$OUT/submit_response_happy.resp.json"

log "capturing GET /v1/messages/:id (complete)"
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/messages/$MSG_ID" | canon > "$OUT/get_message_complete.resp.json"

log "capturing GET /v1/messages/:id/await (complete, immediate)"
curl -sf -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/messages/$MSG_ID/await" | canon > "$OUT/await_complete.resp.json"

# ──── submit response (not_target) ────────────────────────────────────────────

log "capturing POST /v1/messages/:id/response (not_target)"
# Send a fresh message; try to respond as wrong agent.
FRESH_SEND=$(curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"sender_session\":\"$SID1\",\"target\":\"coder\",\"prompt\":\"fresh\",\"hops\":0}" \
  "$BASE/v1/messages")
FRESH_ID=$(echo "$FRESH_SEND" | jq -r '.msg_id')
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"responder_session\":\"$SID1\",\"response\":\"nope\"}" \
  "$BASE/v1/messages/$FRESH_ID/response" | jq '.' > "$OUT/submit_response_not_target.resp.json"

# ──── submit response (message_not_found) ────────────────────────────────────

log "capturing POST /v1/messages/NOTEXIST/response"
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"responder_session\":\"$SID1\",\"response\":\"x\"}" \
  "$BASE/v1/messages/NOTEXIST/response" | jq '.' > "$OUT/submit_response_unknown_msg.resp.json"

# ──── send (hop_limit_exceeded) ───────────────────────────────────────────────

log "capturing POST /v1/messages (hop_limit_exceeded)"
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"sender_session\":\"$SID1\",\"target\":\"coder\",\"prompt\":\"hi\",\"hops\":5}" \
  "$BASE/v1/messages" | jq '.' > "$OUT/send_message_hop_limit.resp.json"

# ──── send (target_not_found) ─────────────────────────────────────────────────

log "capturing POST /v1/messages (target_not_found)"
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"sender_session\":\"$SID1\",\"target\":\"ghost\",\"prompt\":\"hi\",\"hops\":0}" \
  "$BASE/v1/messages" | jq '.' > "$OUT/send_message_target_not_found.resp.json"

# ──── send (ambiguous_target) ─────────────────────────────────────────────────

log "capturing POST /v1/messages (ambiguous_target)"
SID5="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
SID6="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
for pair in "$SID5:dup" "$SID6:dup"; do
  sid="${pair%%:*}"; nm="${pair#*:}"
  curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"project\":\"dup_proj\",\"session_id\":\"$sid\",\"name\":\"$nm\",
         \"purpose\":\"x\",\"model\":\"m\",\"color\":\"#fff\",\"cwd\":\"/tmp\",\"explicit\":false}" \
    "$BASE/v1/agents/register" > /dev/null
done
curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"dup_proj\",\"session_id\":\"$SID5\",\"name\":\"dup\",
       \"purpose\":\"x\",\"model\":\"m\",\"color\":\"#fff\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  "$BASE/v1/agents/register" > /dev/null
# SID5 as sender, target="dup" (ambiguous because dup and dup2 both registered).
SID_SENDER="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"dup_proj\",\"session_id\":\"$SID_SENDER\",\"name\":\"sender_x\",
       \"purpose\":\"x\",\"model\":\"m\",\"color\":\"#fff\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  "$BASE/v1/agents/register" > /dev/null
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"dup_proj\",\"sender_session\":\"$SID_SENDER\",\"target\":\"dup\",\"prompt\":\"hi\",\"hops\":0}" \
  "$BASE/v1/messages" | canon > "$OUT/send_message_ambiguous.resp.json"

# ──── delete agent (happy) ────────────────────────────────────────────────────

log "capturing DELETE /v1/agents/:sid (happy)"
SID_DEL="$(cat /dev/urandom | tr -dc 'A-HJKMNP-TV-Z0-9' | head -c26)"
curl -sf -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"del_proj\",\"session_id\":\"$SID_DEL\",\"name\":\"mortal\",
       \"purpose\":\"x\",\"model\":\"m\",\"color\":\"#fff\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  "$BASE/v1/agents/register" > /dev/null
curl -sf -X DELETE -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents/$SID_DEL?project=del_proj" | jq '.' > "$OUT/delete_agent_happy.resp.json"

# ──── delete agent (not found) ────────────────────────────────────────────────

log "capturing DELETE /v1/agents/NOTEXIST"
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents/NOTEXIST?project=default" | jq '.' > "$OUT/delete_agent_not_found.resp.json"

log "done — fixtures written to $OUT/"
