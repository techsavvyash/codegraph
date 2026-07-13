# AWS External-Service Indexing for CodeGraph

> **One-line goal:** Teach CodeGraph to see the **external AWS services** each Tazapay service
> depends on — starting with **AWS Cognito in the `account` service** — the same way it already
> sees synchronous gRPC/HTTP calls and async SQS events. "Which services call Cognito, from which
> functions, using which operations, and which of those are MFA flows?" becomes a graph query, not
> tribal knowledge.

> **Audience:** the worker LLM implementing this feature in the `codegraph` repo. Read this
> end-to-end before writing code. It is deliberately prescriptive: it names every file, line
> anchor, node/edge, and registry entry you need. Where a decision is *not* pinned, it says so
> explicitly (look for **DECISION** / **NOT SPECIFIED**).

---

## 1. Context — why we are doing this

CodeGraph today models three kinds of edges leaving a function:

- **Synchronous cross-service** — gRPC/HTTP call sites resolved to the concrete handler in the
  target service (`libs/indexer-go/pipeline/cross_service_resolver.go`, `GRPCCall`/`HTTPCall` nodes).
- **Async events** — `queue.SendSQSMsg(...)` publish sites → `OutboxCall` node → `EventType` hub →
  `ROUTED_TO` consumers (`libs/indexer-go/static/event_call_detector.go` + the resolver pass). See
  `docs/async-event-indexing-plan.md`.
- **Database** — `DBCall` nodes.

What is **completely invisible today** is the boundary where our code leaves Tazapay entirely and
calls a **managed cloud service**. The `_cross_service.yaml` KB only records this as prose
(`infrastructure.message_broker: "SQS (AWS) + Kafka"`). No node, no edge. So nobody — human or LLM —
can answer:

- *"Which services talk to AWS Cognito, and from where?"*
- *"Every place we call Cognito `InitiateAuth` — is issuer/pool validation consistent?"*
- *"Do any MFA verification flows actually hit Cognito, or is factor verification all local?"*
- *"When we later add SQS/SES: what is our full AWS blast-radius per service?"*

**Intended outcome:** a `Function → ExternalCall → ExternalService(aws:cognito)` chain, fully
navigable in Neo4j and (optionally) via one MCP tool, mirroring the existing async-event resolver
design so it feels native to the codebase. Cognito ships first; **SQS and SES are additive
registry rows**, not new detection code (see §5, §11).

---

## 2. The mental model (read this first)

Think of an external service as **a utility company** the codebase plugs into:

```
   CALLER FUNCTION            CALL SITE                     PROVIDER SERVICE (hub)
   (account svc)          (one file:line)               "aws:cognito" (shared)

   SignIn() ─────CALLS_API──▶ ExternalCall ────USES_SERVICE────▶ ┌───────────────┐
                              {op: InitiateAuth,                  │  aws:cognito  │
                               variant: USER_PASSWORD_AUTH,       │   (hub node)  │◀── every Cognito
                               wrapper: auth.SignIn,              └───────────────┘    call site in
                               file, line}                                             every service
```

- A **caller function** invokes an external service.
- Each invocation is a **call site** = one `ExternalCall` node (service-scoped, per file:line).
- The **provider service** (`aws:cognito`) is a **shared hub** node (NOT service-scoped), so *every*
  Cognito call from *every* service connects to the **same** node — that is what makes "who uses
  Cognito?" a one-hop query.

This is the **exact same two-tier shape** as the async-event feature
(`OutboxCall` call-site node → `EventType` hub node). We reuse that architecture wholesale; only the
detector's matching dictionary and the hub semantics change.

---

## 3. Ground truth — how the code actually works today

> Verified by reading the `account` service, the `grpc-framework` wrapper packages, and the
> `settlement`/`notification` services. Every path/line below is real. **Do not re-derive these — trust
> them, but re-confirm a line number before editing since services evolve.**

### 3.1 The single most important fact

**No Tazapay service calls the AWS SDK directly.** Every AWS interaction goes through a thin wrapper
package in the shared `grpc-framework` module. The service code only ever sees:

```go
auth.SignIn(ctx, clientID, email, password)     // Cognito  — pkg "…/grpc-framework/client/auth"
queue.SendSQSMsg(ctx, queueURL, msg)            // SQS      — pkg "…/grpc-framework/client/queue"
email.SendRaw(ctx, rawBytes)                    // SES      — pkg "…/grpc-framework/client/email"
```

The raw SDK client (`cognito.Client`, `sqs.Client`, `ses.Client`, all `aws-sdk-go-v2`) is
constructed and invoked **only inside the framework wrapper** (e.g. `_cognitoClient.InitiateAuth(...)`
in `grpc-framework/client/auth/cognito.go:110`).

**Consequence for the indexer (critical):**

1. When we index the `account` repo, the framework is an external module — the raw SDK leaf calls are
   **not in the indexed tree**. The **detection anchor must be the wrapper call**
   (`auth.<Method>`), not the SDK call.
2. A naive matcher for `cognitoidentityprovider.<X>()` would find **ZERO** Cognito usages in
   `account` — the only raw-SDK references there are *type* references
   (`*cognito.RespondToAuthChallengeOutput`, `*cognitoidentityprovider.InitiateAuthOutput`), which
   are **not** call expressions and must **not** be counted. Match the wrapper package by **import
   path**.

### 3.2 The Cognito wrapper surface (`grpc-framework/client/auth`)

Package `github.com/tazapay/grpc-framework/client/auth`, imported **un-aliased** as `auth` in every
`account` file. Each exported function wraps exactly one Cognito SDK operation:

| Wrapper fn (`auth.X`)              | Underlying Cognito SDK op            | Variant / AuthFlow      |
|------------------------------------|--------------------------------------|-------------------------|
| `SignIn`                           | `InitiateAuth`                       | `USER_PASSWORD_AUTH`    |
| `InitiateCustomAuth`               | `InitiateAuth`                       | `CUSTOM_AUTH`           |
| `GetAccessTokenUsingRefreshToken`  | `InitiateAuth`                       | `REFRESH_TOKEN`         |
| `RespondToAuthChallenge`           | `RespondToAuthChallenge`             | `CUSTOM_CHALLENGE`      |
| `Signup`                           | `SignUp`                             | —                       |
| `SendVerificationCode`             | `GetUserAttributeVerificationCode`   | —                       |
| `ConfirmVerificationCode`          | `VerifyUserAttribute`                | —                       |
| `SignOut`                          | `GlobalSignOut`                      | —                       |
| `UpdateUserAttribute`              | `AdminUpdateUserAttributes`          | admin                   |
| `ForgotPassword`                   | `ForgotPassword`                     | —                       |
| `ConfirmForgotPassword`            | `ConfirmForgotPassword`              | —                       |
| `DisableUser`                      | `AdminDisableUser`                   | admin                   |
| `EnableUser`                       | `AdminEnableUser`                    | admin                   |
| `GetUser`                          | `AdminGetUser`                       | admin                   |
| `GetUsers`                         | `ListUsers`                          | —                       |
| `ChangePassword`                   | `ChangePassword`                     | —                       |
| `AdminUserGlobalSignOut`           | `AdminUserGlobalSignOut`             | admin                   |

> The wrapper's real SDK call lives in `grpc-framework@<ver>/client/auth/cognito.go` behind the
> `cognitoProviderAPI` interface (client built by `cognito.NewFromConfig(session.NewAWS(), …)`,
> `InitCognito()`). You do **not** need to index the framework for the Cognito milestone — but see
> the optional deep-link in §7.7.

### 3.3 Cognito call sites in `account` (the ground-truth graph we must reproduce)

Every one of these is a `auth.<Method>(ctx, …)` call. `(file:line)` → enclosing function:

| Wrapper fn                         | Call site (account repo)                              | Enclosing function            |
|------------------------------------|-------------------------------------------------------|-------------------------------|
| `SignIn`                           | `service/grpc/v1/signin.go:72`                        | `SignIn` (gRPC)               |
| `SignIn`                           | `service/grpc/v1/signup.go:379`                       | `autoSignInAfterSignup`       |
| `GetAccessTokenUsingRefreshToken`  | `utils/mfa.go:184`                                    | `GetAccessToken` (public)     |
| `GetAccessTokenUsingRefreshToken`  | `utils/mfa.go:239`                                    | `getPrivateCognitoToken`      |
| `InitiateCustomAuth`               | `service/grpc/v1/mfa_authentication.go:325`           | `performCustomAuthFlow`       |
| `RespondToAuthChallenge`           | `service/grpc/v1/mfa_authentication.go:342`           | `performCustomAuthFlow`       |
| `Signup`                           | `service/grpc/v1/signup.go:313`                       | `SignUp` (gRPC)               |
| `UpdateUserAttribute`              | `service/grpc/v1/signup.go:410`                       | `SignUp`                      |
| `UpdateUserAttribute`              | `utils/auth_challenge.go:880`                         | `updatePhoneNumberInCognito`  |
| `SendVerificationCode`             | `service/grpc/v1/signup.go:424`                       | `SignUp`                      |
| `SendVerificationCode`             | `service/http/v3/user_helper.go:37`                   | resend-verification helper    |
| `ConfirmVerificationCode`          | `service/http/v3/verify_email.go:227`                 | `VerifyEmail` (HTTP)          |
| `ForgotPassword`                   | `service/grpc/v1/forgot_password.go:87`               | `ForgotPassword`              |
| `ConfirmForgotPassword`            | `service/grpc/v1/confirm_password.go:91`              | `ConfirmPassword`             |
| `SignOut`                          | `service/http/v3/signout.go:63`                       | `SignOut` (HTTP)              |
| `GetUsers`                         | `utils/mfa.go:978`                                    | `GetUsernameFromCognito`      |

Wrapper fns present but **not** called by `account`: `DisableUser`, `EnableUser`, `GetUser`,
`ChangePassword`, `AdminUserGlobalSignOut`. (The registry still lists them — they just won't produce
nodes until a call site appears.)

### 3.4 MFA sub-map (the user asked us to pay special attention here)

**Key finding: MFA *factor verification* is local, not Cognito.** TOTP/SMS/WebAuthn config lives in
Postgres (`config_user_mfa`, `config_mfa_webauthn`, `config_mfa_sms`); challenge sessions live in
Redis; WebAuthn assertions use the framework `webauthn` client; SMS OTP is generated locally and
sent via the framework `sms` client (**not** Cognito SMS_MFA). Cognito's native MFA APIs
(`AssociateSoftwareToken`, `VerifySoftwareToken`, `SetUserMFAPreference`) are **not used at all** —
they are not even on the wrapper interface.

The **only** MFA flow that reaches Cognito is **usernameless WebAuthn login → token issuance**:

```
VerifyMFAChallenge (mfa_authentication.go)
  → handleLoginWithWebauthn
    → performCustomAuthFlow(ctx, privateClientID, email)   // the Cognito touchpoint
        → utils.GetUsernameFromCognito(email)   → auth.GetUsers            (ListUsers)
        → auth.InitiateCustomAuth(...)                                     (InitiateAuth CUSTOM_AUTH)
        → auth.RespondToAuthChallenge(...)                                 (RespondToAuthChallenge)
```

Plus two peripheral MFA-adjacent touches: `updatePhoneNumberInCognito` (`auth.UpdateUserAttribute`,
during SMS-factor registration) and refresh-token issuance (`GetAccessTokenUsingRefreshToken`).

**This is itself a valuable graph fact.** Once indexed, a query like *"MFA RPCs that reach an
external service"* should return essentially only `VerifyMFAChallenge`'s WebAuthn branch — proving
the rest of MFA is self-contained. See §7.6 for the optional `mfa` tagging that makes this a
first-class filter; if we skip tagging, the same answer is still reachable by traversing from the
MFA RPC functions to `ExternalCall` nodes.

### 3.5 Two things a call-site matcher will NOT catch (document them; don't silently drop)

1. **Cognito Hosted-UI OAuth over HTTP.** `service/http/v3/social_signup.go:634` `getAccessToken`
   does a plain `apiclient.Post(ctx, tokenURL, …)` to `env.CognitoOAuthTokenURL`. This is a Cognito
   interaction but a raw HTTP POST — no `auth.` selector. **Detection strategy (Phase 2, low
   confidence):** flag functions that read the env key `cognito.oauth_token_url`. Until then, log it
   as a known blind spot (see §10 "no silent caps").
2. **Config, not calls.** `env/*.yaml` `cognito:` blocks and `env/service.go` keys
   (`cognito.client_id`, `cognito.user_pool_id`, `cognito.oauth_token_url`, …) tell us the pool/client
   identity. These are **config evidence**, optionally attached to the hub node as provenance (§7.5),
   not call sites.

### 3.6 SQS & SES (forward-looking — for the registry design, not this milestone)

| Provider | Service wrapper call            | Wrapper import path                              | Underlying SDK op (leaf, framework only)     |
|----------|---------------------------------|--------------------------------------------------|----------------------------------------------|
| SQS      | `queue.SendSQSMsg`, `queue.SendDelaySQSMsg` (+ `ReceiveSQSMsg`/`DeleteSQSMsg`/…) | `…/grpc-framework/client/queue` | `sqs.Client.SendMessage` (aws-sdk-go-v2)      |
| SES      | `email.SendRaw`                 | `…/grpc-framework/client/email`                  | `ses.Client.SendRawEmail` (aws-sdk-go-v2)     |
| Kinesis  | (bonus) `_kinesisClient.PutRecords` | `…/grpc-framework/client/queue` (kinesis.go) | `kinesis.Client.PutRecords`                   |

> **Overlap warning for SQS:** `queue.SendSQSMsg` is **already detected** by the async-event pipeline
> (it produces an `OutboxCall` node). When we add SQS to this feature, do **NOT** create a second,
> competing detector for the same call. Instead, at the existing `OutboxCall` emission site
> (`event_call_detector.go` `writeEmissions`), *also* MERGE the `aws:sqs` hub and a
> `USES_SERVICE` edge from the `OutboxCall`. That keeps event-routing semantics and
> infra-dependency in one place. This is a **DECISION to confirm** with the maintainer before the SQS
> phase (§11).

---

## 4. Graph model (nodes, edges, keys)

Mirror the `OutboxCall`/`EventType` design exactly.

### 4.1 Nodes

**`ExternalCall`** — one node per external call site (service-scoped). Fits the existing `*Call`
family (`GRPCCall`, `HTTPCall`, `DBCall`, `OutboxCall`).

| Prop            | Example                     | Notes                                              |
|-----------------|-----------------------------|----------------------------------------------------|
| `provider`      | `aws`                       | constant for now                                   |
| `externalService` | `cognito`                 | `cognito` \| later `sqs` \| `ses`                  |
| `operation`     | `InitiateAuth`              | underlying SDK op (from registry)                  |
| `variant`       | `USER_PASSWORD_AUTH`        | optional sub-op (AuthFlow / `admin` / "")          |
| `wrapperFunc`   | `SignIn`                    | the `auth.X` fn actually called                    |
| `callerService` | `account`                   | owning service (scope)                             |
| `filePath`      | `service/grpc/v1/signin.go` | repo-relative                                      |
| `line`          | `72`                        |                                                    |
| `name`          | `account:cognito.InitiateAuth` | caption (mirror OutboxCall `service + ":" + …`)  |

**`ExternalService`** — the shared hub (NOT service-scoped), one per `(provider, service)`.

| Prop        | Example    | Notes                                     |
|-------------|------------|-------------------------------------------|
| `provider`  | `aws`      |                                           |
| `name`      | `cognito`  | `cognito` \| `sqs` \| `ses`               |
| `category`  | `identity` | `identity` \| `messaging` \| `email` (registry-supplied) |
| `displayName` | `AWS Cognito` | for rendering                          |

### 4.2 Edges

| Edge            | From → To                       | Reuse/new | Props                                   |
|-----------------|---------------------------------|-----------|-----------------------------------------|
| `CALLS_API`     | `Function`/`Method` → `ExternalCall` | **reuse** `CallsAPIRel` | (same as OutboxCall) — so node summaries & flow spines pick it up for free |
| `USES_SERVICE`  | `ExternalCall` → `ExternalService` | **new** `UsesServiceRel = "USES_SERVICE"` | `operation`, `variant`, `wrapperFunc` |
| `DEPENDS_ON_EXTERNAL` | `Service` → `ExternalService` | **new** `DependsOnExternalRel` | `operations` (list), `callCount` — aggregated in the resolver pass (§7.4) |

> **Why reuse `CALLS_API` for Function→ExternalCall:** the async-event feature already wires
> `Function -[CALLS_API]-> OutboxCall`, and both `node_summary_generator.go` and the flow-spine
> generator traverse `CALLS_API`. Reusing it means external calls appear in function summaries and
> flows with **zero** extra wiring in those subsystems.

### 4.3 Node keys (`libs/core-models-go/nodekey.go`)

```go
// ExternalCallNodeKey — service-scoped, mirrors OutboxCallNodeKey.
// "externalcall:{service}:{filePath}:{provider}:{externalService}:{operation}:{line}"
func ExternalCallNodeKey(service, filePath, provider, extService, operation string, line int) string {
    return fmt.Sprintf("externalcall:%s:%s:%s:%s:%s:%d",
        service, filePath, provider, extService, operation, line)
}

// ExternalServiceNodeKey — NOT service-scoped (shared hub), mirrors EventTypeNodeKey.
// "extsvc:{provider}:{name}"
func ExternalServiceNodeKey(provider, name string) string {
    return "extsvc:" + provider + ":" + name
}
```

> There is a leftover **`SDKCallNodeKey(target) = "sdkcall:" + target`** at `nodekey.go:117-119`
> (unused). **DECISION:** prefer the two explicit keys above over repurposing `SDKCallNodeKey`, and
> delete the unused helper (or leave it — confirm with maintainer). Rationale: our node is
> service-scoped and multi-field; `sdkcall:{target}` cannot express that identity.

---

## 5. The External-Call Registry (the actual "teaching")

This is the heart of the feature and the reason SQS/SES become trivial later. A single
data table maps **(wrapper import path, wrapper function name) → external operation**. Detection is
then a table lookup, not bespoke code per service.

New file `libs/indexer-go/static/external_call_registry.go`:

```go
package static

// externalOp describes what one wrapper function call means at the AWS boundary.
type externalOp struct {
    Provider    string // "aws"
    Service     string // "cognito" | "sqs" | "ses"
    Category    string // "identity" | "messaging" | "email"
    Operation   string // underlying SDK op, e.g. "InitiateAuth"
    Variant     string // AuthFlow / "admin" / "" — optional sub-op
    WrapperFunc string // "SignIn" (redundant w/ map key, kept for node props)
}

// externalCallRegistry: import path -> wrapper func name -> op.
// Matching is by IMPORT PATH (not the local alias token), because a raw-alias match is unreliable.
var externalCallRegistry = map[string]map[string]externalOp{
    // ---- Phase 1: AWS Cognito (account service) ----
    "github.com/tazapay/grpc-framework/client/auth": {
        "SignIn":                          {"aws", "cognito", "identity", "InitiateAuth", "USER_PASSWORD_AUTH", "SignIn"},
        "InitiateCustomAuth":              {"aws", "cognito", "identity", "InitiateAuth", "CUSTOM_AUTH", "InitiateCustomAuth"},
        "GetAccessTokenUsingRefreshToken": {"aws", "cognito", "identity", "InitiateAuth", "REFRESH_TOKEN", "GetAccessTokenUsingRefreshToken"},
        "RespondToAuthChallenge":          {"aws", "cognito", "identity", "RespondToAuthChallenge", "CUSTOM_CHALLENGE", "RespondToAuthChallenge"},
        "Signup":                          {"aws", "cognito", "identity", "SignUp", "", "Signup"},
        "SendVerificationCode":            {"aws", "cognito", "identity", "GetUserAttributeVerificationCode", "", "SendVerificationCode"},
        "ConfirmVerificationCode":         {"aws", "cognito", "identity", "VerifyUserAttribute", "", "ConfirmVerificationCode"},
        "SignOut":                         {"aws", "cognito", "identity", "GlobalSignOut", "", "SignOut"},
        "UpdateUserAttribute":             {"aws", "cognito", "identity", "AdminUpdateUserAttributes", "admin", "UpdateUserAttribute"},
        "ForgotPassword":                  {"aws", "cognito", "identity", "ForgotPassword", "", "ForgotPassword"},
        "ConfirmForgotPassword":           {"aws", "cognito", "identity", "ConfirmForgotPassword", "", "ConfirmForgotPassword"},
        "DisableUser":                     {"aws", "cognito", "identity", "AdminDisableUser", "admin", "DisableUser"},
        "EnableUser":                      {"aws", "cognito", "identity", "AdminEnableUser", "admin", "EnableUser"},
        "GetUser":                         {"aws", "cognito", "identity", "AdminGetUser", "admin", "GetUser"},
        "GetUsers":                        {"aws", "cognito", "identity", "ListUsers", "", "GetUsers"},
        "ChangePassword":                  {"aws", "cognito", "identity", "ChangePassword", "", "ChangePassword"},
        "AdminUserGlobalSignOut":          {"aws", "cognito", "identity", "AdminUserGlobalSignOut", "admin", "AdminUserGlobalSignOut"},
    },

    // ---- Phase 2 (do NOT enable until §3.6 overlap decision): AWS SES ----
    // "github.com/tazapay/grpc-framework/client/email": {
    //     "SendRaw": {"aws", "ses", "email", "SendRawEmail", "", "SendRaw"},
    // },
    // ---- Phase 2: AWS SQS — prefer wiring at the existing OutboxCall site, not here (see §3.6) ----
}
```

Adding SES later = uncomment three lines. That is the whole point.

---

## 6. Detection algorithm (AST)

New file `libs/indexer-go/static/external_call_detector.go`, modeled on `event_call_detector.go`.
It runs **per function**, inside the existing AST walk (§7.3).

**Per-file setup (once):** build an import map for the file being parsed:

```go
// aliasToPath: local package name (alias, or last path segment if un-aliased) -> full import path.
func buildImportMap(file *ast.File) map[string]string
```

Rationale: `account` imports the wrapper un-aliased, so the local token is `auth`; but matching the
literal token `auth` is fragile (another package could be named `auth`). Resolve the selector's
package **ident → import path** and match the path against the registry. This is the robustness the
research explicitly called out.

**`DetectInFunction(funcDecl, funcID, importMap, callerService, filePath)`** — single pass:

```
ast.Inspect(funcDecl.Body):
  for each node n that is *ast.CallExpr:
      sel, ok := n.Fun.(*ast.SelectorExpr); if !ok: continue        // must be pkg.Method form
      pkgIdent, ok := sel.X.(*ast.Ident);   if !ok: continue        // sel.X must be a package ident
      importPath, ok := importMap[pkgIdent.Name]; if !ok: continue  // resolve alias -> path
      fnTable, ok := externalCallRegistry[importPath]; if !ok: continue
      op, ok := fnTable[sel.Sel.Name]; if !ok: continue             // registry hit == external call

      line := fset.Position(n.Pos()).Line
      ecKey := ExternalCallNodeKey(callerService, filePath, op.Provider, op.Service, op.Operation, line)
      esKey := ExternalServiceNodeKey(op.Provider, op.Service)

      buffer.addExternalCall(ecKey, props{provider, externalService, operation, variant,
                                          wrapperFunc, callerService, filePath, line,
                                          name: callerService + ":" + op.Service + "." + op.Operation})
      buffer.addCallsAPIEdge(funcID, ecKey, line)                   // Function -> ExternalCall
      buffer.addExternalServiceNode(esKey, props{provider, name, category, displayName})
      buffer.addUsesServiceEdge(ecKey, esKey, props{operation, variant, wrapperFunc})
```

**Correctness notes (must-haves):**

- **Only `*ast.CallExpr` callees are inspected**, so type references like
  `*cognito.RespondToAuthChallengeOutput` (a `*ast.SelectorExpr` in type position, never a call
  callee) are **naturally excluded**. Add a test that asserts this (§9).
- **`sel.X` must be a plain package `*ast.Ident`.** The service-layer calls are always `auth.Foo`.
  We deliberately do NOT try to match receiver-method forms (`client.SendMessage`) here — those are
  the framework-internal SDK leaves and live in a different module (§7.7 optional).
- **No dynamic-value resolution needed for Phase 1.** Unlike the event pipeline, the
  wrapper-fn → operation mapping is fully static (the registry hardcodes it), so we do **not** need
  `constResolver`/switch enumeration. (We *may* later resolve `clientID` public-vs-private constants
  onto the node as a `variant` refinement — **NOT SPECIFIED**, skip for now.)

---

## 7. File-by-file implementation checklist

> Anchors are from the current tree; re-confirm before editing. This mirrors the async-event
> feature's 9-touchpoint footprint.

### 7.1 `libs/core-models-go/node.go`
- Add `ExternalCallNode NodeType = "ExternalCall"` and `ExternalServiceNode NodeType = "ExternalService"`
  to the `const` block (near `OutboxCallNode`, line ~23).
- Add two structs embedding `BaseNode` (mirror `OutboxCall` at ~219 and `EventType` at ~234) with the
  props from §4.1.
- Add `case ExternalCallNode:` / `case ExternalServiceNode:` arms to `NodeFactory` (near the
  `EventTypeNode` arm ~465).

### 7.2 `libs/core-models-go/nodekey.go`
- Add `ExternalCallNodeKey` and `ExternalServiceNodeKey` (§4.3). Consider deleting the unused
  `SDKCallNodeKey` (lines 117-119) — confirm first.

### 7.3 `libs/core-models-go/relationship.go`
- Add consts `UsesServiceRel RelationshipType = "USES_SERVICE"` and
  `DependsOnExternalRel RelationshipType = "DEPENDS_ON_EXTERNAL"` (const block, lines 6-68).
- Add typed structs `UsesServiceRelationship` / `DependsOnExternalRelationship` embedding
  `BaseRelationship` (mirror `EmitsEventRelationship` at ~242).
- Add `case UsesServiceRel:` / `case DependsOnExternalRel:` to `RelationshipFactory` (~305).

### 7.4 New: `libs/indexer-go/static/external_call_registry.go`
- The registry from §5.

### 7.5 New: `libs/indexer-go/static/external_call_detector.go`
- `ExternalCallDetector` struct (holds `callBuffer`, `serviceName`), `buildImportMap`, and
  `DetectInFunction` (§6).
- *(Optional provenance)* attach pool/client-id evidence to the hub: if any indexed file reads
  `env.CognitoUserPoolID`/`CognitoClientID`, set `ExternalService.configKeys`. **NOT SPECIFIED** —
  defer unless asked.

### 7.6 `libs/indexer-go/static/call_node_buffer.go`
- Add buffered maps `externalCalls`, `externalServices`, and edge maps `usesService` (mirror
  `outboxCalls`/`eventTypes`/`emitsEvent`, lines ~32-45).
- Add `addExternalCall`, `addExternalServiceNode`, `addUsesServiceEdge` (mirror `addEventTypeNode`
  ~74, `addEmitsEventEdge` ~79).
- In `flush` (~138): `flushNodes` for `ExternalCall` and `ExternalService`; flush `USES_SERVICE` via
  the existing `flushRelsByBothNodeKeys` helper (~254) — it already does the
  `MATCH (a{nodeKey}),(b{nodeKey}) MERGE (a)-[r:TYPE]->(b) SET r += props` pattern; just pass the new
  rel type. Reuse `addCallsAPIEdge`/the CALLS_API flush unchanged for Function→ExternalCall.
- *(Optional MFA tag)* if the enclosing function's file/name matches the MFA set
  (`mfa_*.go`, `auth_challenge.go`, functions under `MFAServiceServer`), set `ExternalCall.flowTag =
  "mfa"`. Cheap, makes §3.4 a one-property filter. **DECISION:** include it — it directly serves the
  user's MFA focus. If unsure, ship without and rely on traversal.

### 7.7 `libs/indexer-go/static/scip_indexer.go`
- In `runASTRPCDetection` (~1413): construct `ExternalCallDetector`, `SetCallNodeBuffer(callBuffer)`.
- In the per-file walk (~1451-1491), build the import map for the file, and after
  `eventDet.DetectInFunction(...)` (~1486) add
  `extDet.DetectInFunction(funcDecl, funcID, importMap, si.serviceName, relPath)`.
- No new flush call needed — `callBuffer.flush` (~1497) already persists everything buffered.
- *(Optional deep-link, only if the framework module is ever co-indexed)* detect the SDK leaf inside
  `grpc-framework/client/auth/cognito.go` (`_cognitoClient.<Op>`) and link
  `auth.<WrapperFn>` function → `ExternalCall`/`ExternalService`. Out of scope for the account
  milestone.

### 7.8 `libs/indexer-go/pipeline/cross_service_resolver.go`
- In `Resolve(ctx)` (~42), after the async passes, add `resolveExternalDependencies(ctx)`:
  a single Cypher aggregation creating the `Service → DEPENDS_ON_EXTERNAL → ExternalService`
  rollup so dependency-map queries and the KB see it directly:

```cypher
MATCH (ec:ExternalCall)-[:USES_SERVICE]->(es:ExternalService)
MATCH (svc:Service {name: ec.callerService})
WITH svc, es, collect(DISTINCT ec.operation) AS ops, count(ec) AS n
MERGE (svc)-[r:DEPENDS_ON_EXTERNAL]->(es)
SET r.operations = ops, r.callCount = n, r.resolvedAt = $now
```

  (This is optional — the hub's incoming `USES_SERVICE` edges already answer "who uses Cognito"; the
  rollup is a convenience mirroring `linkOutboxCallsToDownstream`.)

### 7.9 `libs/indexer-go/static/node_summary_generator.go`
- In `fetchDirectEffects` (~165-262), add a Cypher block mirroring the events block (~238):

```cypher
MATCH (n)-[:CALLS_API]->(ec:ExternalCall)
RETURN ec.provider AS provider, ec.externalService AS service, ec.operation AS operation
```
  so a function summary reads e.g. *"…calls AWS Cognito (InitiateAuth)…"*. Extend `NodeEffects`
  (~311) with an `External []string` slice and render it.

### 7.10 `libs/schema-go/schema.go`
- Add UNIQUE constraint `externalservice_nodekey_unique` on `ExternalService.nodeKey` and
  `externalcall_nodekey_unique` on `ExternalCall.nodeKey`; BTREE indexes on
  `ExternalService(name, scopeId)` and `ExternalCall(externalService, scopeId)` — mirror the
  `eventtype_*` constraints/indexes added in the async-event commit.

### 7.11 *(Optional)* MCP surface — `apps/mcp-server-go/`
- Add a tool `codegraph_external_dependencies(service?)` → lists `ExternalService` hubs and their
  callers/operations, or fold into the existing `codegraph_service_dependency_map`. Defer unless
  requested — **NOT SPECIFIED**.

---

## 8. Worked example — what the graph must contain after indexing `account`

```
(:Function {name:"SignIn"})
   -[:CALLS_API]->(:ExternalCall {externalService:"cognito", operation:"InitiateAuth",
                                  variant:"USER_PASSWORD_AUTH", wrapperFunc:"SignIn",
                                  callerService:"account", filePath:"service/grpc/v1/signin.go", line:72})
   -[:USES_SERVICE {operation:"InitiateAuth", variant:"USER_PASSWORD_AUTH"}]->
     (:ExternalService {provider:"aws", name:"cognito", displayName:"AWS Cognito"})

(:Service {name:"account"})-[:DEPENDS_ON_EXTERNAL {operations:[...], callCount:16}]->(:ExternalService{name:"cognito"})
```

Acceptance queries (run in §9):

```cypher
// 1. Who uses Cognito?  -> expect exactly ["account"]
MATCH (ec:ExternalCall)-[:USES_SERVICE]->(:ExternalService{name:"cognito"})
RETURN collect(DISTINCT ec.callerService);

// 2. Every Cognito call site  -> expect the 16 rows from §3.3
MATCH (f)-[:CALLS_API]->(ec:ExternalCall)-[:USES_SERVICE]->(:ExternalService{name:"cognito"})
RETURN f.name, ec.operation, ec.filePath, ec.line ORDER BY ec.filePath, ec.line;

// 3. MFA flows that reach Cognito (uses optional flowTag or the MFA function set)
MATCH (f)-[:CALLS_API]->(ec:ExternalCall{externalService:"cognito"})
WHERE ec.flowTag = "mfa"     // or: f.name IN [...MFA RPC names...]
RETURN DISTINCT f.name, ec.operation;   // expect performCustomAuthFlow's InitiateAuth+RespondToAuthChallenge, updatePhoneNumberInCognito, refresh-token
```

---

## 9. Testing & verification

- **Unit (detector):** a Go testdata fixture with (a) a real `auth.SignIn(ctx,…)` call → asserts one
  `ExternalCall` + hub + edges; (b) a **type-only** `var x *cognito.InitiateAuthOutput` → asserts
  **zero** nodes (the false-positive guard); (c) an un-registered `auth.SomethingElse` → zero nodes;
  (d) a same-named `foo.SignIn` where `foo` is a different import path → zero nodes (import-path, not
  token, matching).
- **Registry table test:** every value's `Service`/`Provider` non-empty; no duplicate wrapper-fn keys.
- **Integration (Neo4j):** index the real `account` repo, run the three acceptance queries in §8;
  assert query 1 == `["account"]` and query 2 returns the 16 known call sites. Treat any count drift
  as a signal to update §3.3, not to loosen the assertion.
- `make lint` clean; `make test` green.

---

## 10. Guardrails (no silent gaps)

- **Log the known blind spots.** At end of an `account` index run, emit a one-line notice that the
  **Cognito Hosted-UI OAuth HTTP path** (`social_signup.go` `getAccessToken`) and the
  **framework-internal SDK leaves** are intentionally not captured in Phase 1 (§3.5, §3.6). Silent
  omission would read as "Cognito fully mapped" when it is not.
- **Registry drift.** If a wrapper gains a new exported function and a service calls it, it silently
  produces no node. Add a CI check (or a periodic audit) that greps each service for
  `auth.<Ident>(` selectors whose `<Ident>` is not in the registry and warns.

---

## 11. Phasing & open decisions

**Phase 1 (this milestone): AWS Cognito in `account`.** §7.1–7.10. Ship the registry with only the
`client/auth` block enabled.

**Phase 2: SES.** Uncomment the `client/email` row in the registry. One integration test against
`notification`.

**Phase 3: SQS.** Do **not** add a competing detector. Wire the `aws:sqs` hub + `USES_SERVICE` edge
at the existing `OutboxCall` emission site so SQS infra-dependency and event-routing stay unified.

**Open decisions to confirm with the maintainer (record answers in the KB):**
1. **SQS overlap (§3.6):** reuse the `OutboxCall` site vs. a separate `ExternalCall`? *Recommended:
   reuse.*
2. **`SDKCallNodeKey` leftover (§4.3):** delete or keep?
3. **MFA `flowTag` (§7.6):** include the tag now? *Recommended: yes — it directly serves the stated
   MFA focus.*
4. **`DEPENDS_ON_EXTERNAL` rollup (§7.8) and MCP tool (§7.11):** in-scope for Phase 1 or defer?
5. **Config provenance (§7.5):** attach pool/client-id env keys to the hub? *Not specified — default:
   defer.*

> These are flagged because they are **not** dictated by the code and were **not** confirmed by the
> requester; a future session must not assume them settled.

---

## 12. Appendix — reference file anchors

| Purpose                          | File (codegraph)                                            |
|----------------------------------|------------------------------------------------------------|
| Node types + factory             | `libs/core-models-go/node.go`                              |
| Node keys                        | `libs/core-models-go/nodekey.go`                           |
| Relationship types + factory     | `libs/core-models-go/relationship.go`                      |
| Async-event detector (template)  | `libs/indexer-go/static/event_call_detector.go`            |
| Buffered writes                  | `libs/indexer-go/static/call_node_buffer.go`               |
| AST detection wiring             | `libs/indexer-go/static/scip_indexer.go` (`runASTRPCDetection` ~1413) |
| Cross-service resolver pass      | `libs/indexer-go/pipeline/cross_service_resolver.go`       |
| Node summaries                   | `libs/indexer-go/static/node_summary_generator.go`         |
| Neo4j write primitives           | `libs/neo4j-go/client.go`                                  |
| Schema constraints/indexes       | `libs/schema-go/schema.go`                                 |
| Reference design (async events)  | `docs/async-event-indexing-plan.md`                        |

| Purpose                          | File (account / framework)                                 |
|----------------------------------|------------------------------------------------------------|
| Cognito wrapper (SDK leaves)     | `grpc-framework/client/auth/cognito.go`                    |
| Cognito env keys                 | `account/env/service.go` (`cognito.*`)                     |
| MFA touchpoint                   | `account/service/grpc/v1/mfa_authentication.go` (`performCustomAuthFlow`) |
| SQS wrapper                      | `grpc-framework/client/queue/sqs.go` (`SendSQSMsg` → `SendMessage`) |
| SES wrapper                      | `grpc-framework/client/email/ses.go` (`SendRaw` → `SendRawEmail`) |
