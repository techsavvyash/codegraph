# CodeGraph Analysis Playbook

A repeatable pattern for using the CodeGraph MCP (over the Tazapay fleet) for day-to-day
analysis and implementation. Following this order is what gets **maximum return**: the
graph gives you the *map* cheaply; source reading gives you the *why* expensively — so
**map first, read last, and only read what the map says matters.**

> Benchmark basis: on the ConfirmPayout trace, a single `rpc_dependencies` call replaced
> ~21 recursive source reads. On CreatePayout, the MCP path cost ~⅓ of pure source
> reading. But pure source reading won on *precision* — because the graph knows structure,
> not business logic. This playbook combines both: graph for breadth, targeted source for
> depth.

---

## The pattern: Orient → Map → Drill → Verify

### Step 1 — Orient (1 call, no source)
- `codegraph_list_services` if the target service is unknown.
- Identify the entry point: the RPC/handler or event consumer the task starts from.
- Write one line stating what you expect to find — so gaps are visible at the end.

### Step 2 — Map the structure (prefer ONE fat call, no source)
Pick the tool by the question you're asking:

| Question | Tool |
|---|---|
| "What does this RPC touch?" (sync + async downstream, DB tables, events) | `codegraph_rpc_dependencies` |
| "Who calls this RPC?" (cross-service, upstream) | `codegraph_api_callers` |
| "Ordered steps / conditions / transaction / parallelism?" | `codegraph_rpc_anatomy` |
| "Event fan-out and consumers?" | `codegraph_event_flow` |
| "Who depends on this service?" | `codegraph_service_dependency_map` |
| "Find a symbol / handler by name or intent" | `codegraph_search` / `codegraph_hybrid_search` |

- **Read the `Does:` line first** — it carries the node's one-hop effects (tables
  written/read, services called, events emitted).
- Record every `file:line` citation the tool emits. These are your verification anchors.
- `rpc_dependencies` splits **sync fan-out** from **Async Downstream (after self-consumed
  events)** — never merge them; async is what happens *after* an event fires.

### Step 3 — Drill for the "why" (SELECTIVE source, ≤3 reads)
The graph tells you WHAT and WHERE, not always WHY. For the **1–3 call sites** where a
branch, condition, or business rule actually decides the outcome:
- `codegraph_get_source` on that specific handler/function (disambiguate with `service=`).
- Read **only** those bodies. Do not open a file the map didn't point you to.

### Step 4 — Verify & report gaps (non-negotiable)
- Confirm each claim against a `file:line` from Step 2/3. Flag any you couldn't verify.
- State sync vs async explicitly.
- **Watch for silent blind spots.** A missing edge produces a confident-but-incomplete
  answer with no error. If a flow you'd expect to be messy looks suspiciously clean,
  say so rather than assert completeness. Known thin areas: env-var-sourced queue URLs,
  Kinesis/CDC transport, Redis pipelines, FIX egress, raw `apiclient` HTTP.
- Output shape: `entry point → sync fan-out → async downstream → DB writes → events`,
  each with a citation.

---

## Guardrails
- **Trust the graph for structure** (calls, tables, events, reachability).
  **Trust source for logic** (conditions, ordering, error handling, business rules).
- If a name collides across services, pass `service=` — never guess.
- `pol` = repo `payment-orchestration`, `sol` = repo `settlement-orchestration`
  (proto path segment ≠ repo dir). Map before `get_source`.
- Never present a graph result as "the complete picture" without Step 4.

---

## Copy-paste prompt

```
You have the codegraph MCP over the Tazapay fleet. Task: <TASK>.
Follow Orient → Map → Drill → Verify. Do NOT read source or grep until you have the
structural map, and then only for the 1-3 call sites where a condition/business rule
decides the outcome. Record file:line for every claim. Separate sync from async. Flag
anything the graph couldn't confirm rather than asserting completeness.
```
