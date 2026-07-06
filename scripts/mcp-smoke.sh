#!/usr/bin/env bash
# mcp-smoke.sh — stdio smoke test for the codegraph MCP server (RFC-006
# verification matrix). Builds the server, speaks JSON-RPC 2.0 over stdio,
# and asserts the 9-tool surface from RFC-004 responds correctly:
#   codegraph_schema, codegraph_find, codegraph_expand, codegraph_path,
#   codegraph_cypher, codegraph_source, codegraph_entry_points,
#   codegraph_flows, codegraph_render
#
# Usage: scripts/mcp-smoke.sh
#
# Exit codes: 0 = all PASS (or a clean SKIP with no FAILs), 1 = at least one
# FAIL, or the server failed to build.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MCP_BIN="/tmp/codegraph-mcp-smoke"
BUILD_LOG="$(mktemp)"
STDERR_LOG="$(mktemp)"
RENDER_OUT="/tmp/codegraph-mcp-smoke-render.html"

EXPECTED_TOOLS="codegraph_cypher codegraph_entry_points codegraph_expand codegraph_find codegraph_flows codegraph_path codegraph_render codegraph_schema codegraph_source"

# result table: array of "name|STATUS|detail"
declare -a RESULTS=()
add_result() { RESULTS+=("$1|$2|$3"); }

cleanup() {
  if [[ -n "${MCP_PID:-}" ]] && kill -0 "$MCP_PID" 2>/dev/null; then
    exec {MCP_IN}>&- 2>/dev/null || true
    sleep 0.2
    kill -0 "$MCP_PID" 2>/dev/null && kill -TERM "$MCP_PID" 2>/dev/null
    wait "$MCP_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

print_table() {
  echo ""
  echo "=== MCP smoke test results ==="
  printf "%-24s %-8s %s\n" "TOOL" "STATUS" "DETAIL"
  printf "%-24s %-8s %s\n" "----" "------" "------"
  local any_fail=0
  for row in "${RESULTS[@]}"; do
    IFS='|' read -r name status detail <<<"$row"
    printf "%-24s %-8s %s\n" "$name" "$status" "$detail"
    [[ "$status" == "FAIL" ]] && any_fail=1
  done
  echo ""
  return $any_fail
}

# --- 0. dependency check -----------------------------------------------
if ! command -v jq >/dev/null 2>&1; then
  echo "SKIPPED (jq not installed) — mcp-smoke.sh requires jq to parse JSON-RPC responses."
  exit 0
fi

# --- 1. build ------------------------------------------------------------
echo "Building MCP server..."
cd "$REPO_ROOT"
if go build -o "$MCP_BIN" ./apps/mcp-server-go >"$BUILD_LOG" 2>&1; then
  echo "  build OK (go.work workspace mode)"
elif (cd apps/mcp-server-go && GOWORK=off go build -o "$MCP_BIN" .) >>"$BUILD_LOG" 2>&1; then
  echo "  build OK (GOWORK=off, module-local)"
else
  echo "BUILD FAILED:"
  cat "$BUILD_LOG"
  exit 1
fi

# --- 2. Neo4j reachability ------------------------------------------------
NEO4J_HOST="${NEO4J_HOST:-localhost}"
NEO4J_PORT="${NEO4J_PORT:-7687}"
if ! (exec 3<>"/dev/tcp/${NEO4J_HOST}/${NEO4J_PORT}") 2>/dev/null; then
  echo "SKIPPED (Neo4j down): ${NEO4J_HOST}:${NEO4J_PORT} not reachable. The server" \
       "log.Fatalf's on connect failure, so no protocol exchange is possible."
  exit 0
fi
exec 3>&- 3<&- 2>/dev/null || true
echo "Neo4j reachable at ${NEO4J_HOST}:${NEO4J_PORT}."

# --- 3. start server as a coprocess --------------------------------------
coproc MCP { "$MCP_BIN" 2>"$STDERR_LOG"; }
MCP_PID="$MCP_PID"
MCP_IN="${MCP[1]}"
MCP_OUT="${MCP[0]}"

# mcp_send_recv REQUEST_JSON WANT_ID [DEADLINE_SECS] -> sets REPLY_LINE,
# returns 0 when a response whose .id == WANT_ID arrives within the deadline,
# 1 on timeout/EOF/dead process. Lines with a different id (e.g. a late reply
# to an earlier timed-out call) or no id (notifications) are discarded so a
# single slow tool cannot desync every subsequent request/response pair.
mcp_send_recv() {
  local req="$1" want_id="$2" deadline="${3:-30}"
  REPLY_LINE=""
  if ! kill -0 "$MCP_PID" 2>/dev/null; then
    return 1
  fi
  if ! echo "$req" >&"$MCP_IN" 2>/dev/null; then
    return 1
  fi
  local end=$((SECONDS + deadline)) line rid
  while ((SECONDS < end)); do
    if IFS= read -r -t 2 line <&"$MCP_OUT"; then
      rid=$(echo "$line" | jq -r '.id // empty' 2>/dev/null)
      if [[ "$rid" == "$want_id" ]]; then
        REPLY_LINE="$line"
        return 0
      fi
      echo "  (discarding out-of-order line with id='${rid:-none}')" >&2
    elif ! kill -0 "$MCP_PID" 2>/dev/null; then
      return 1
    fi
  done
  return 1
}

# mcp_notify REQUEST_JSON — fire-and-forget (no id => server sends no reply).
mcp_notify() {
  echo "$1" >&"$MCP_IN" 2>/dev/null || true
}

server_died_msg() {
  echo "server process exited/unresponsive; stderr tail: $(tail -c 300 "$STDERR_LOG" | tr '\n' ' ')"
}

# --- 4. initialize ---------------------------------------------------------
req_init=$(jq -nc '{jsonrpc:"2.0", id:1, method:"initialize", params:{protocolVersion:"2024-11-05", capabilities:{}, clientInfo:{name:"mcp-smoke", version:"0.1"}}}')
if mcp_send_recv "$req_init" 1 && echo "$REPLY_LINE" | jq -e '.result.serverInfo.name' >/dev/null 2>&1; then
  add_result "initialize" "PASS" "serverInfo=$(echo "$REPLY_LINE" | jq -c .result.serverInfo)"
else
  add_result "initialize" "FAIL" "$(server_died_msg)"
  print_table
  exit 1
fi

notify_init=$(jq -nc '{jsonrpc:"2.0", method:"notifications/initialized"}')
mcp_notify "$notify_init"

# --- 5. tools/list ----------------------------------------------------------
req_list=$(jq -nc '{jsonrpc:"2.0", id:2, method:"tools/list"}')
if mcp_send_recv "$req_list" 2; then
  if ! echo "$REPLY_LINE" | jq -e '.result.tools' >/dev/null 2>&1; then
    add_result "tools/list" "FAIL" "malformed response: $(echo "$REPLY_LINE" | head -c 200)"
    print_table
    exit 1
  fi
  tool_count=$(echo "$REPLY_LINE" | jq '.result.tools | length')
  actual_tools=$(echo "$REPLY_LINE" | jq -r '.result.tools[].name' | sort | tr '\n' ' ' | sed 's/ $//')
  expected_sorted=$(echo "$EXPECTED_TOOLS" | tr ' ' '\n' | sort | tr '\n' ' ' | sed 's/ $//')
  if [[ "$tool_count" -eq 9 && "$actual_tools" == "$expected_sorted" ]]; then
    add_result "tools/list" "PASS" "9 tools: $actual_tools"
  else
    add_result "tools/list" "FAIL" "expected 9 tools [$expected_sorted], got $tool_count: [$actual_tools]"
  fi
else
  add_result "tools/list" "FAIL" "$(server_died_msg)"
  print_table
  exit 1
fi

# --- 6. tools/call, one per tool -------------------------------------------
call_id=10
call_tool() {
  local name="$1" args_json="$2" deadline="${3:-30}"
  call_id=$((call_id + 1))
  local req
  req=$(jq -nc --argjson id "$call_id" --arg name "$name" --argjson args "$args_json" \
    '{jsonrpc:"2.0", id:$id, method:"tools/call", params:{name:$name, arguments:$args}}')
  if ! mcp_send_recv "$req" "$call_id" "$deadline"; then
    add_result "$name" "FAIL" "$(server_died_msg)"
    return 1
  fi
  if ! echo "$REPLY_LINE" | jq -e . >/dev/null 2>&1; then
    add_result "$name" "FAIL" "non-JSON response: $(echo "$REPLY_LINE" | head -c 200)"
    return 1
  fi
  if echo "$REPLY_LINE" | jq -e '.error' >/dev/null 2>&1; then
    add_result "$name" "FAIL" "JSON-RPC protocol error: $(echo "$REPLY_LINE" | jq -c .error)"
    return 1
  fi
  if ! echo "$REPLY_LINE" | jq -e '.result.content' >/dev/null 2>&1; then
    add_result "$name" "FAIL" "no .result.content in response: $(echo "$REPLY_LINE" | head -c 200)"
    return 1
  fi
  local is_error text_preview
  is_error=$(echo "$REPLY_LINE" | jq -r '.result.isError // false')
  text_preview=$(echo "$REPLY_LINE" | jq -r '.result.content[0].text // ""' | head -c 120 | tr '\n' ' ')
  add_result "$name" "PASS" "isError=$is_error text=\"${text_preview}...\""
  # stash full reply for callers that need to extract ids
  LAST_REPLY="$REPLY_LINE"
  return 0
}

# schema {}
call_tool "codegraph_schema" '{}'

# find {"label":"Service","limit":5}
call_tool "codegraph_find" '{"label":"Service","limit":5}'
find_reply="$LAST_REPLY"
service_id=$(echo "$find_reply" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.results[0].node_id // empty' 2>/dev/null)

id_a="" id_b=""
if [[ -n "$service_id" ]]; then
  id_a="$service_id"
  # try to get a second, distinct id for path
  id_b=$(echo "$find_reply" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.results[1].node_id // empty' 2>/dev/null)
fi

if [[ -z "$id_a" ]]; then
  # fall back to an unfiltered find to get any node id at all (e.g. graph has
  # no Service label, or the graph is empty).
  call_tool "codegraph_find (fallback, no label)" '{"limit":5}'
  fb_reply="$LAST_REPLY"
  id_a=$(echo "$fb_reply" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.results[0].node_id // empty' 2>/dev/null)
  id_b=$(echo "$fb_reply" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.results[1].node_id // empty' 2>/dev/null)
fi

# expand
edge_from="" edge_to=""
if [[ -n "$id_a" ]]; then
  call_tool "codegraph_expand" "$(jq -nc --arg id "$id_a" '{node_id:$id, rel_types:["CONTAINS","CALLS"], direction:"both", depth:1, limit:10}')"
  # Harvest a connected pair from expand's edge list so the path check below
  # exercises a pair that provably has a path (a bare two-ids-from-find pair
  # can legitimately return "no path", which would mask a broken path tool).
  edge_from=$(echo "$LAST_REPLY" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.edges[0].from // empty' 2>/dev/null)
  edge_to=$(echo "$LAST_REPLY" | jq -r '.result.content[0].text' 2>/dev/null | jq -r '.edges[0].to // empty' 2>/dev/null)
else
  add_result "codegraph_expand" "WARN" "graph appears empty — no node_id available from find, skipped"
fi

# path — prefer the expand-derived connected pair (strong assertion: a path
# MUST be found); fall back to two arbitrary find ids (weak: any well-formed
# response passes, since the pair may genuinely be unconnected).
if [[ -n "$edge_from" && -n "$edge_to" && "$edge_from" != "$edge_to" ]]; then
  if call_tool "codegraph_path" "$(jq -nc --arg a "$edge_from" --arg b "$edge_to" '{from_id:$a, to_id:$b, rel_types:["CONTAINS","CALLS"], max_hops:6, shortest:true}')"; then
    path_text=$(echo "$LAST_REPLY" | jq -r '.result.content[0].text // ""' 2>/dev/null)
    if echo "$path_text" | grep -qi "no path"; then
      # overwrite the PASS row: these two nodes share a direct edge, so
      # "no path found" means the tool is broken.
      unset 'RESULTS[-1]'
      add_result "codegraph_path" "FAIL" "reported 'no path' between directly-connected nodes $edge_from -> $edge_to"
    fi
  fi
elif [[ -n "$id_a" && -n "$id_b" && "$id_a" != "$id_b" ]]; then
  call_tool "codegraph_path" "$(jq -nc --arg a "$id_a" --arg b "$id_b" '{from_id:$a, to_id:$b, rel_types:["CONTAINS","CALLS"], max_hops:6, shortest:true}')"
else
  add_result "codegraph_path" "WARN" "need two distinct node ids from find; graph too sparse/empty, skipped"
fi

# cypher
call_tool "codegraph_cypher" '{"query":"MATCH (n) RETURN count(n) AS c"}'

# source
call_tool "codegraph_source" '{"symbol_name":"main"}'

# entry_points
call_tool "codegraph_entry_points" '{"limit":5}'

# flows — multi-strategy seed detection is the slowest tool on a large graph
# (known RFC-006 Phase 2 target); give it a longer deadline than the default.
call_tool "codegraph_flows" '{"limit":3}' 120

# render
call_tool "codegraph_render" "$(jq -nc --arg out "$RENDER_OUT" '{query:"MATCH (a)-[r]->(b) RETURN a, r, b LIMIT 5", out_path:$out}')"
if [[ -f "$RENDER_OUT" ]]; then
  size=$(wc -c <"$RENDER_OUT" | tr -d ' ')
  echo "  (render wrote $RENDER_OUT, ${size} bytes)"
fi

print_table
exit $?
