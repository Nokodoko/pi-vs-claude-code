# testdata — coms-go golden fixtures

## Layout

```
testdata/
├── golden/          # JSON fixtures for every HTTP route (req + resp pairs)
│   ├── health.req.json
│   ├── health.resp.json
│   ├── register_happy.req.json
│   ├── register_happy.resp.json
│   ├── register_no_auth.req.json
│   ├── register_no_auth.resp.json
│   ├── list_agents_empty.resp.json
│   ├── list_agents_populated.resp.json
│   ├── send_message_happy.req.json / .resp.json
│   ├── send_message_hop_limit.resp.json
│   ├── send_message_target_not_found.resp.json
│   ├── send_message_ambiguous.resp.json
│   ├── get_message_queued.resp.json
│   ├── get_message_complete.resp.json
│   ├── await_complete.resp.json
│   ├── await_timeout.resp.json
│   ├── submit_response_happy.resp.json
│   ├── submit_response_not_target.resp.json
│   ├── submit_response_unknown_msg.resp.json
│   ├── delete_agent_happy.resp.json
│   ├── delete_agent_not_found.resp.json
│   └── sse_pool_snapshot.txt
└── frontmatter/
    └── sample-agent.md
```

## Placeholder convention

Dynamic fields are canonicalized to deterministic placeholders before commit:

| Placeholder   | Replaces                                       |
|---------------|------------------------------------------------|
| `"<ulid>"`    | Any 26-char Crockford ULID                     |
| `"<iso>"`     | Any RFC3339 / ISO8601 timestamp                |
| `"<bearer>"`  | Any secret bearer token                        |
| `"<sse_url>"` | The `/v1/events?project=…&session_id=…` path   |
| `"<response>"`| Any serialized response payload                |

**No real bearer tokens are committed.** The integration tests substitute a known test token at request time.

## Regenerating fixtures from the TS reference server

The fixtures in `golden/` are checked into git so `go test -tags=integration` does **not** require Bun. Regeneration is only needed when the TS server wire protocol changes.

### Prerequisites

- Bun ≥ 1.3.2 (`bun --version`)
- `jq` (for canonicalization)

### Procedure

```bash
# From the repo root:
cd /path/to/pi-vs-claude-code

# Start TS server on a known port with a fixed token.
PI_COMS_NET_AUTH_TOKEN=test-token PI_COMS_NET_PORT=43210 \
  bun scripts/coms-net-server.ts &
TS_PID=$!
sleep 1

TOKEN=test-token
BASE=http://127.0.0.1:43210
OUT=extensions/coms-go/testdata/golden

# --- Health ---
curl -s $BASE/health | jq '.' > $OUT/health.resp.json

# --- Register (happy path) ---
SID1=$(cat /proc/sys/kernel/random/uuid | tr -d '-' | head -c 26)
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"session_id\":\"$SID1\",\"name\":\"planner\",
       \"purpose\":\"Plans the work\",\"model\":\"claude-opus-4-7\",
       \"color\":\"#36F9F6\",\"cwd\":\"/tmp\",\"explicit\":false}" \
  $BASE/v1/agents/register | jq '.' > $OUT/register_happy.resp.json

# --- Register (no auth) ---
curl -s -X POST \
  -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"session_id\":\"anon\",\"name\":\"planner\"}" \
  $BASE/v1/agents/register | jq '.' > $OUT/register_no_auth.resp.json

# --- List agents (populated) ---
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents?project=default" | jq '.' > $OUT/list_agents_populated.resp.json

# --- Send message (hop limit) ---
SID2=$(cat /proc/sys/kernel/random/uuid | tr -d '-' | head -c 26)
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"sender_session\":\"$SID1\",\"target\":\"coder\",
       \"prompt\":\"hi\",\"hops\":5}" \
  $BASE/v1/messages | jq '.' > $OUT/send_message_hop_limit.resp.json

# --- Submit response (unknown message) ---
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"project\":\"default\",\"responder_session\":\"$SID1\",\"response\":\"x\"}" \
  $BASE/v1/messages/NOTEXIST/response | jq '.' > $OUT/submit_response_unknown_msg.resp.json

# --- Delete agent (happy path) ---
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents/$SID1?project=default" | jq '.' > $OUT/delete_agent_happy.resp.json

# --- Delete agent (not found) ---
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/agents/NOTEXIST?project=default" | jq '.' > $OUT/delete_agent_not_found.resp.json

# Canonicalize dynamic values.
# Replace ULIDs (26-char Crockford), ISO timestamps, bearer token.
find $OUT -name '*.json' | while read f; do
  sed -E \
    -e 's/"[0-9A-HJKMNP-TV-Z]{26}"/"<ulid>"/g' \
    -e 's/"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:\.Z+-]+"/"<iso>"/g' \
    -i "$f"
done

kill -INT $TS_PID
```

After regeneration, review the diff with `git diff extensions/coms-go/testdata/golden/` before committing.
