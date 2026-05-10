# CodeGraph Architecture Documentation

> Auto-generated from the CodeGraph Code Property Graph using MCP tools.
> These documents describe the architecture, entry points, data flows, and call graphs
> discovered by structural analysis of the codebase.

## Documents

| Document | Description |
|----------|-------------|
| [Overview](./01-overview.md) | High-level architecture, service map, and dependency graph |
| [Entry Points](./02-entry-points.md) | Structurally-detected entry points across all 4 tiers |
| [Flow Spines](./03-flow-spines.md) | Call chain documentation showing how requests flow through the code |
| [Call Graph Analysis](./04-call-graphs.md) | Deep call graph traces for key functions |
| [MCP Tools Reference](./05-mcp-tools.md) | Reference for the 20 MCP tools available for code intelligence |

## How This Was Generated

These documents were generated using CodeGraph's own MCP tools (with service-scoped filtering):

- **`codegraph_get_entry_points`** — Discovers entry points across 4 structural tiers (supports `scope_id`, `service_name`)
- **`codegraph_generate_flows`** — Generates flow spines from entry points through call chains (supports `scope_id`, `service_name`)
- **`codegraph_trace_call_graph`** — Traces upstream/downstream call graphs from specific functions (supports `scope_id`, `service_name`)
- **`codegraph_service_architecture`** — Maps services and their dependencies
- **`codegraph_list_services`** — Lists all indexed services with metadata

## Regenerating

To regenerate this documentation after reindexing:

```bash
# Ensure Neo4j is running and codebase is indexed
make docker-up
make index-self-scip

# Use Claude Code with MCP tools
claude -p "Use codegraph MCP tools to regenerate arch-docs/" --allowedTools "mcp__codegraph__*"
```
