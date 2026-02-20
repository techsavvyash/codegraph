# Bundled SCIP Indexers

This guide describes how to make **SCIP indexers a built-in part of CodeGraph**, so users do not have to install `scip-go`, `scip-typescript`, `scip-python`, `scip-java`, etc. separately.

## Why This Matters

Enterprise adoption depends on predictable, low-friction setup.

Today CodeGraph’s SCIP indexing path (see `pkg/indexer/static/scip_indexer.go`) expects the language-specific indexer binary to exist in `PATH` and returns install instructions if it is missing.

To support PR overlays and agent workflows at scale, CodeGraph should be able to:

- acquire the correct indexer automatically
- cache it
- execute it reliably in CI and developer machines

## Design Goals

1. **No separate manual installs** of SCIP indexer binaries.
2. **Deterministic versioning**: the same CodeGraph version runs the same indexer version.
3. **Safe downloads**: checksum verification, TLS, explicit allowlist.
4. **Cross-platform**: Linux/macOS/Windows where possible.
5. **Transparent troubleshooting**: `codegraph indexers status` shows what is installed.

## Key Constraint (What “Bundled” Can and Cannot Mean)

Some SCIP indexers are true native binaries with GitHub releases.
Others are wrappers around language toolchains.

CodeGraph can bundle the indexer executable, but may still require:

- Java build tools (Gradle/Maven/sbt) for `scip-java`
- Node projects to have `node` available (unless CodeGraph also bundles a Node runtime)
- Python projects to have Python available (unless CodeGraph bundles a Python runtime)

The goal is: **no separate indexer install step**, even if language toolchains are still required.

## Recommended Implementation: Indexer Manager

Add a small subsystem responsible for indexer acquisition and execution.

### Interface

```go
type IndexerManager interface {
  EnsureInstalled(ctx context.Context, lang Language) (IndexerBinary, error)
  Status(ctx context.Context) ([]IndexerStatus, error)
}

type IndexerBinary struct {
  Path    string
  Version string
}
```

### Cache layout

Store binaries under:

```
~/.codegraph/indexers/<indexer-name>/<version>/<os>-<arch>/<binary>
```

Examples:

- `~/.codegraph/indexers/scip-go/v0.1.23/linux-amd64/scip-go`
- `~/.codegraph/indexers/scip-java/v0.10.0/linux-amd64/scip-java`

### Acquisition

Prefer GitHub releases (or pinned URLs) and verify:

- SHA256 checksum
- executable permission
- basic `--version` output

### Execution

Update `pkg/indexer/static/scip_indexer.go` to:

1. call `EnsureInstalled()` for the language
2. use the returned `Path` instead of `exec.LookPath`
3. keep the current language-specific flags

## CLI UX

Add commands:

- `codegraph indexers status`
- `codegraph indexers install --language go,typescript,java`
- optionally: `codegraph index scip ... --auto-install-indexers` (default true)

## Verification

### Unit tests

1. Resolution chooses correct OS/arch artifact.
2. Missing indexer triggers download.
3. Corrupt download fails checksum and is not used.

### Integration test

Run SCIP indexing in an environment where indexers are not installed in `PATH`:

1. delete `~/.codegraph/indexers`
2. run `codegraph index scip ...`
3. verify indexer is downloaded and indexing succeeds

## Release Considerations

Two common patterns:

### Pattern A: download-on-demand (recommended initially)

- small CodeGraph binary
- download indexers on first use
- cache for subsequent runs

Pros:

- avoids shipping a huge multi-platform release artifact
- faster iteration on indexer version bumps

### Pattern B: fully bundled release artifacts

- ship CodeGraph + indexer binaries per OS/arch bundle

Pros:

- no downloads at runtime

Cons:

- release size grows quickly
- more complex packaging and CI

## Relationship to PR Overlays

PR overlay indexing relies on being able to run SCIP indexers in CI with minimal setup. Bundling/indexer management is what makes the 3–4 minute overlay SLA realistic.
