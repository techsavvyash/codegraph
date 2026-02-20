# Generated Context (Flows, Docstrings, PR Summaries)

This guide defines what “auto-generated documentation” means for CodeGraph in an enterprise context engine:

- generate **docstring suggestions** and **flow summaries** as part of PR indexing
- store them as first-class knowledge units
- use them to improve search and agent context

This is not “generate a static docs website”. It is **generate queryable context** that can be cited and linked back to code and source documents.

## Why Generate Context

Three problems show up at scale:

1. Docstrings and READMEs lag reality.
2. Business context exists in external tools (Confluence/GDocs) and rarely points to exact code.
3. “What does this change impact?” is not answerable by grep.

Generated context addresses this by producing:

- PR-scoped summaries for review
- flow spines for onboarding/debugging
- docstring suggestions to keep code self-describing

## Generated Knowledge Units

### 1. Pull request summary

Create one `GeneratedDoc` per PR:

- changes overview
- touched services/modules
- likely impact surface (APIs, topics, DB tables)
- suggested review focus areas

Suggested type:

- `GeneratedDoc.type = 'pr_summary'`

### 2. Flow summaries

Flow summaries are the bridge from business questions to execution.

Examples:

- “Create user flow”
- “Payment capture flow”
- “Webhook ingestion flow”

For a PR, generate flow summaries for:

- changed entrypoints (API endpoints, message consumers, cron jobs)
- changed exported methods in core services

Suggested type:

- `GeneratedDoc.type = 'flow_summary'`

### 3. Docstring suggestions

Generate docstring suggestions (not mandatory auto-commit) for:

- changed exported functions/methods/classes
- functions with missing/low-quality docstrings

Suggested type:

- `GeneratedDoc.type = 'docstring_suggestion'`

## Graph Storage Model

Store generated context as graph nodes with provenance.

Nodes:

- `PullRequest {tenantId, repo, prId, headSha, baseSha, createdAt}`
- `GeneratedDoc {nodeKey, type, textHash, vectorId, scope, scopeId, model, createdAt}`

Relationships:

- `(GeneratedDoc)-[:DOCUMENTS]->(Function|Class|APIEndpoint|Service|Flow)`
- `(GeneratedDoc)-[:DERIVED_FROM]->(PullRequest)`
- `(GeneratedDoc)-[:DERIVED_FROM]->(DocumentChunk)` (when external docs inform the summary)
- `(GeneratedDoc)-[:DERIVED_FROM]->(Symbol|Function|File)` (when code is the primary source)

Key requirement: `DERIVED_FROM` must exist so enterprise users can audit “why did the system claim this?”.

## PR Overlay Behavior

Generated context should be scoped the same way as code:

- store generated context into the PR overlay (`scope='pr'`, `scopeId=prId`)
- after merge, promote to main during the promotion job

This ensures:

- reviewers see the PR-specific summary
- main remains stable

## Using Generated Context In Retrieval

Generated docs are highly valuable retrieval targets:

- flow summaries embed better than raw code
- PR summaries answer “what changed” queries directly
- docstring suggestions capture intent not present in code

Recommended retrieval behavior:

1. always include `GeneratedDoc` in vector search candidates
2. when the query seems like “flow”, boost `flow_summary` results
3. when query includes “why changed / what changed”, boost `pr_summary`

## Verification

1. Run PR overlay indexing and confirm a `PullRequest` node is created.
2. Confirm `GeneratedDoc` nodes are created for:
   - `pr_summary`
   - `flow_summary`
   - `docstring_suggestion`
3. Confirm every `GeneratedDoc` has at least one `DERIVED_FROM` edge.
4. Confirm vector search can retrieve generated flow summaries.
5. Confirm promotion on merge moves generated context into main scope.
