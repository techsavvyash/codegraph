# AWS S3 Indexing for CodeGraph

> **Companion to** [`aws-external-service-indexing-plan.md`](./aws-external-service-indexing-plan.md).
> That parent plan defines the reusable machinery — the `ExternalCall` / `ExternalService`
> two-tier node model (§4), the node keys (§4.3), the `externalCallRegistry` table (§5), the AST
> detection algorithm (§6), and the 9-file implementation footprint (§7). **This document does not
> restate that machinery.** It specifies only what is S3-specific: the wrapper surface, the
> registry rows, the S3-only detection hazards, and the "map the bucket" dimension the user asked
> for. Read the parent first; treat S3 as a **data + guardrail extension**, not new plumbing.

---

## 1. Context — what "map the S3 bucket usage" means here

The user wants the indexer to recognise S3 usage and map it, after Cognito. Unlike Cognito
(single logical service, one account) S3 usage is:

- **Broad** — 9 services call it (settlement, onboarding, payin, account, report, payout-router,
  genai-orchestration, metadata, bfi), vs. Cognito's single consumer.
- **Bucket-oriented** — the interesting question isn't only "who calls S3?" but "who reads/writes
  **which bucket**?" (e.g. `tazapay-payment-logos`, the payout-document bucket, KYC-doc buckets).
- **Verb-diverse** — read / write / delete / copy / presign, not just one auth flow.

The good news: S3 funnels through **exactly one wrapper package**, and every service calls it with
the same `storage.<Fn>(ctx, …)` package-function convention that the parent plan's detector already
handles. So S3 is **primarily additive registry rows** — but with three S3-only wrinkles that make
it more than a copy-paste (§4).

---

## 2. Ground truth — how S3 is actually called today

### 2.1 The single most important fact (same funnel rule as Cognito)

No Tazapay service imports the AWS S3 SDK directly. All S3 access goes through one wrapper:

```
import "github.com/tazapay/grpc-framework/client/storage"
```

- **Module:** `github.com/tazapay/grpc-framework`
- **Import path (the detection anchor):** `github.com/tazapay/grpc-framework/client/storage`
- **Package name / local token:** `storage` (imported un-aliased everywhere seen)
- **Wrapper source:** `grpc-framework/client/storage/s3.go` (+ `constant.go`)
- **Raw SDK leaf (framework-internal only):** `github.com/aws/aws-sdk-go-v2/service/s3`,
  `.../service/s3/types`, presign client, and `feature/s3/manager` — all confined to `s3.go`.
  A raw-SDK matcher run against a service repo finds **zero** S3 calls (only type references), for
  the same reason it found zero Cognito calls. **Anchor on the wrapper import path.**

### 2.2 The wrapper surface (`grpc-framework/client/storage/s3.go`)

Exported functions fall into **three buckets**. Only the first is an S3 operation.

**(A) S3 OPERATION functions — these are what we index** (verified from wrapper bodies):

| `storage.<Fn>`   | Signature (bucket source)                              | Underlying SDK op(s)                | `operation` | `variant`       |
|------------------|--------------------------------------------------------|-------------------------------------|-------------|-----------------|
| `Upload`         | `(ctx, in *UploadInput)` — bucket in `in.Bucket`       | `PutObject`                         | `PutObject` | `write`         |
| `GetObject`      | `(ctx, bucket, key)`                                   | `GetObject`                         | `GetObject` | `read`          |
| `GetObjectURL`   | `(ctx, bucket, key, contentDisposition, expiry)`       | `GetObject` **+** `PresignGetObject`| `GetObject` | `presign-read`  |
| `PutObjectURL`   | `(ctx, bucket, key, expiry)`                           | `PresignPutObject`                  | `PutObject` | `presign-write` |
| `HeadObject`     | `(ctx, bucket, key)`                                   | `HeadObject`                        | `HeadObject`| `metadata`      |
| `IsObjectExists` | `(ctx, bucket, key)` — wraps `HeadObject`              | `HeadObject`                        | `HeadObject`| `exists`        |
| `Delete`         | `(ctx, bucket, key)`                                   | `DeleteObject`                      | `DeleteObject` | `delete`     |
| `CopyObject`     | `(ctx, in *TransferObjectInput)` — src/dst in struct   | `CopyObject` **+** `HeadObject`(wait)| `CopyObject`| `copy`          |

**(B) DI / setup functions — MUST be excluded (huge false-positive risk):**
`InitS3`, `SetS3`, `SetPresign`, `SetWait`. These wire the client for tests/bootstrap and perform
**no network call**. Repo-wide counts: `storage.SetS3(` **398**, `storage.SetPresign(` **146**,
`storage.SetWait(` **91**, `storage.InitS3(` 1. If the detector matched "any exported func from the
storage import path," these ~636 sites (mostly test wiring) would swamp the graph.

**(C) Pure local helpers — MUST be excluded (no S3 boundary crossed):**
`SanitizeFileName`, `ValidateFileExtension`, `ValidateMIMEType`. String/validation utilities only.
(`validateFileType` is unexported and *does* call `HeadObject` internally, but only via the operation
funcs above — we index it transitively through `CopyObject`, never directly.)

> **This is the S3-specific lesson:** the `storage` package co-locates operations, DI setup, and
> validators. The parent plan's registry is already an **allowlist** (§5) — good — but for `storage`
> the allowlist is *load-bearing*, not a nicety. Never widen detection to "all funcs from this path."

### 2.3 Real call-site distribution (repo-wide, non-test)

`GetObjectURL` 34 · `Upload` 8 · `CopyObject` 6 · `GetObject` 4 · `PutObjectURL` 2 ·
`IsObjectExists` 1 · `Delete` 1. Presigned-download URL generation dominates real usage.

Consuming services (by file count): settlement 17 · onboarding 12 · payin 8 · account 8 ·
report 5 · payout-router 2 · genai-orchestration 2 · metadata 1 · bfi 1.

Representative anchor to reproduce in tests:
`settlement/utils/document.go:108` — `storage.GetObjectURL(ctx, u.S3DocBucket, docObj.Reference…)`.

---

## 3. Graph model delta (reuse parent §4 verbatim, plus bucket)

Nodes/edges/keys are **unchanged** from the parent plan:

- `ExternalService` hub = **single** `extsvc:aws:s3` (`provider=aws`, `name=s3`,
  `category=storage`, `displayName="AWS S3"`). **One hub for all buckets** (see §4.3 for why we do
  NOT make each bucket a hub).
- `ExternalCall` node per call site, `ExternalCallNodeKey(service, filePath, "aws", "s3", operation, line)`.
- `Function -[CALLS_API]-> ExternalCall -[USES_SERVICE]-> ExternalService`, optional
  `Service -[DEPENDS_ON_EXTERNAL]-> ExternalService` rollup — all identical to Cognito.

**S3-only addition — the `bucket` property on `ExternalCall`:**

| Prop             | Example                 | Notes                                                        |
|------------------|-------------------------|--------------------------------------------------------------|
| `bucket`         | `tazapay-payment-logos` | best-effort; empty string when not a static literal          |
| `bucketResolved` | `true` / `false`        | `true` only when extracted from an `*ast.BasicLit`           |
| `objectKeyExpr`  | `docObj.Reference`      | *(optional)* the raw key-arg source text, for human readers  |

Everything else on the node (provider/externalService/operation/variant/wrapperFunc/caller/file/line/
name) is exactly as parent §4.1, with `name = "{service}:s3.{operation}"`.

---

## 4. The three S3-specific detection changes

### 4.1 Registry rows (append to `externalCallRegistry`)

Add one import-path block. Note the extra `BucketArgIndex` field on the op struct (see §4.2).

```go
// ---- Phase (S3): AWS S3 (9 services, via grpc-framework/client/storage) ----
"github.com/tazapay/grpc-framework/client/storage": {
    // fn              provider svc  category   operation      variant          wrapperFunc      bucketArgIdx
    "Upload":         {"aws", "s3", "storage", "PutObject",    "write",         "Upload",         -1}, // in.Bucket (struct)
    "GetObject":      {"aws", "s3", "storage", "GetObject",    "read",          "GetObject",       1},
    "GetObjectURL":   {"aws", "s3", "storage", "GetObject",    "presign-read",  "GetObjectURL",    1},
    "PutObjectURL":   {"aws", "s3", "storage", "PutObject",    "presign-write", "PutObjectURL",    1},
    "HeadObject":     {"aws", "s3", "storage", "HeadObject",   "metadata",      "HeadObject",      1},
    "IsObjectExists": {"aws", "s3", "storage", "HeadObject",   "exists",        "IsObjectExists",  1},
    "Delete":         {"aws", "s3", "storage", "DeleteObject", "delete",        "Delete",          1},
    "CopyObject":     {"aws", "s3", "storage", "CopyObject",   "copy",          "CopyObject",     -1}, // in.SrcBucket/DstBucket (struct)
},
```

> **Do NOT add** `InitS3`/`SetS3`/`SetPresign`/`SetWait`/`SanitizeFileName`/`ValidateFileExtension`/
> `ValidateMIMEType`. Their absence from this allowlist is the guardrail (§2.2 B/C). Add a unit test
> that asserts `storage.SetS3(...)` produces **zero** `ExternalCall` nodes (§6).

This requires adding one field to the `externalOp` struct in the parent plan (§5):
```go
BucketArgIndex int // positional arg index (after ctx) holding the bucket string literal; -1 = inside a struct arg
```
Cognito rows get `-1` (or the field is defaulted); only S3 uses it today.

### 4.2 Best-effort bucket extraction (the "map the bucket" ask)

Inside the detector's per-CallExpr branch (parent §6), after a registry hit, attempt bucket capture:

```
if op.BucketArgIndex >= 0 && op.BucketArgIndex < len(call.Args):
    if lit, ok := call.Args[op.BucketArgIndex].(*ast.BasicLit); ok && lit.Kind == token.STRING:
        bucket        = strings.Trim(lit.Value, "\"")   // e.g. "tazapay-payment-logos"
        bucketResolved = true
    else:
        // arg is an identifier/selector/const (u.S3DocBucket, cfg.Bucket, bucketConst) — cannot
        // statically resolve. Record the source text for humans, leave bucket empty.
        objectKeyExpr  = exprString(call.Args[op.BucketArgIndex]) // optional
        bucketResolved = false
// BucketArgIndex == -1 (Upload/CopyObject): bucket lives in a struct-literal/field arg.
// Phase-1: leave unresolved. (Optional later: if the arg is a *ast.CompositeLit, walk its
// Elts for the "Bucket"/"SrcBucket"/"DstBucket" key with a BasicLit value.)
```

**Expectation to state plainly (no silent gap):** in real code the bucket is almost always a config
field or constant (`u.S3DocBucket`, `docConfig.Bucket`), **not** a literal — so `bucketResolved` will
be `false` at most sites. That is acceptable for the milestone: the operation, verb (variant),
caller, and file:line are still captured, and the `aws:s3` hub still answers "who uses S3?".
Full bucket resolution needs a const/config-propagation pass and is deferred (§7, NOT SPECIFIED).

### 4.3 Why one `aws:s3` hub and not one hub per bucket

Tempting: `ExternalService` per bucket so "who touches `tazapay-payment-logos`?" is one hop. Rejected
for the milestone because bucket is rarely statically known (§4.2) — most call sites would edge to an
`unknown` bucket hub, producing a misleading graph. Instead: **single `aws:s3` hub**, bucket as a
best-effort **prop**. If/when a config-resolution pass lands, we can introduce an optional third tier
`ExternalCall -[TARGETS_BUCKET]-> S3Bucket -[OF_SERVICE]-> ExternalService(aws:s3)` and backfill.
Flagged NOT SPECIFIED — do not build the bucket tier without confirming resolution quality first.

---

## 5. What the detector already handles (no new work)

Because the call convention is `storage.<Fn>(...)` — identical to `auth.<Fn>` — the parent plan's
detection algorithm (§6) needs **no structural change** beyond the registry rows + the bucket-capture
snippet above:

- Import-path resolution (`storage` token → full path) — already built (parent §6, `buildImportMap`).
- `*ast.CallExpr` + `*ast.SelectorExpr` + package-`*ast.Ident` guard — already excludes S3 **type
  references** (`*s3.GetObjectOutput`, `types.NotFound`) naturally.
- `CALLS_API` reuse → S3 calls appear in function summaries and flow spines for free.
- Cross-service resolver rollup (`DEPENDS_ON_EXTERNAL`) — S3 rows flow through unchanged.

---

## 6. Testing & verification (S3-specific asserts)

Add to the detector's test suite (mirror parent §9):

1. **Positive:** a fixture calling `storage.GetObjectURL(ctx, "my-bucket", "k", "", 0)` yields one
   `ExternalCall` (`operation=GetObject`, `variant=presign-read`, `bucket="my-bucket"`,
   `bucketResolved=true`) + `USES_SERVICE`→`extsvc:aws:s3`.
2. **Non-literal bucket:** `storage.GetObject(ctx, u.S3DocBucket, key)` yields the node with
   `bucket=""`, `bucketResolved=false` (NOT dropped).
3. **DI-exclusion (critical):** `storage.SetS3(m)`, `storage.SetPresign(p)`, `storage.SetWait(w)`,
   `storage.InitS3()` produce **zero** `ExternalCall` nodes.
4. **Validator-exclusion:** `storage.ValidateMIMEType(mt)`, `storage.SanitizeFileName(f)` produce
   **zero** nodes.
5. **Type-ref exclusion:** a func with `var out *s3.GetObjectOutput` and no call produces zero nodes.
6. **Struct-arg op:** `storage.Upload(ctx, &storage.UploadInput{Bucket:"b", …})` yields
   `operation=PutObject`, `bucketResolved=false` (Phase-1) — or `true` if the optional composite-lit
   walk (§4.2) is implemented.
7. **Integration sanity:** after indexing `settlement`, `MATCH (:ExternalService{name:"s3"})<-[:USES_SERVICE]-(c:ExternalCall) RETURN c.operation, count(*)`
   returns the verb distribution (expect `GetObject` presign-read dominant).

---

## 7. Phasing & open decisions

**Phasing:** S3 can ship in the **same milestone as Cognito** or immediately after — it reuses all
plumbing. Recommended order once the parent machinery exists: Cognito → **S3** → SES → SQS(reuse
OutboxCall). S3 before SES/SQS because it is the highest-fan-out, highest-value surface (9 services,
document/KYC/logo flows) and exercises the allowlist guardrail hardest.

**Open / NOT SPECIFIED (do not assume settled):**

- **Bucket resolution depth.** Phase-1 captures literals only; `bucketResolved=false` dominates.
  Whether to add a const/config-propagation pass to resolve `u.S3DocBucket` → actual bucket name is
  **NOT SPECIFIED**. Reason to defer: needs data-flow analysis the indexer doesn't have yet.
- **Bucket as its own node tier** (`S3Bucket`) — deferred, gated on resolution quality (§4.3). NOT SPECIFIED.
- **Struct-arg bucket walk** for `Upload`/`CopyObject` (composite-literal field extraction) —
  optional; skip unless a test demands it. NOT SPECIFIED.
- **`variant` taxonomy.** Chosen values (`read`/`write`/`presign-read`/`presign-write`/`metadata`/
  `exists`/`delete`/`copy`) are inferred from wrapper bodies, **not** confirmed by a maintainer or
  TDD. They double as a human-facing verb; revisit if the team wants raw SDK op only.
- **Compound-op modelling.** `GetObjectURL` = GetObject + PresignGetObject and `CopyObject` =
  CopyObject + HeadObject-wait are each modelled as **one** `ExternalCall` (the dominant op), not two.
  Reason: the wrapper presents one logical operation; splitting adds noise. NOT confirmed with maintainer.
- **payment-router S3 open question** (`.knowledge/services/payment-router.yaml:523`): logo URLs are
  static S3/CDN assets; payment-router was **not** found importing `client/storage` in this scan, so
  its presigned-URL utility (if any) is unconfirmed — left open.

---

## 8. Appendix — reference file anchors (re-confirm before editing)

- Wrapper: `grpc-framework/client/storage/s3.go`
  (`Upload`:139, `GetObjectURL`:175, `Delete`:219, `PutObjectURL`:233, `GetObject`:257,
  `HeadObject`:268, `IsObjectExists`:280, `CopyObject`:306; DI setters `InitS3`:96 `SetS3`:121
  `SetPresign`:126 `SetWait`:131; validators `SanitizeFileName`:357 `ValidateFileExtension`:376
  `ValidateMIMEType`:393).
- SDK leaf imports in that file: `aws-sdk-go-v2/service/s3` :18, `.../s3/types` :19.
- Representative service call site: `settlement/utils/document.go:108`.
- All indexer touchpoints: identical 9-file list in parent plan §7 (only the registry file gains rows
  and the `externalOp` struct gains `BucketArgIndex`).
