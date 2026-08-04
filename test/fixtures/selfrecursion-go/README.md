# selfrecursion-go

Regression fixture for the RFC-013 self-loop-drop bug and its downstream
"inDegree = 0 means no external caller" fix.

- `CalledRecursive` is called once, externally, from `main`, and also calls
  itself (self-recursion with an external caller).
- `OrphanRecursive` is exported, calls itself, but has NO external caller at
  all — nothing in the fixture calls it.

Used by test/integration/self_recursion_test.go, which indexes this fixture
and asserts:
  - `CalledRecursive -[:CALLS]-> CalledRecursive` exists (self-loop survives
    collapseToMinLinePerPair — RFC-013 fix #2).
  - `CalledRecursive.inDegree == 1` (the external call from main only — the
    self-loop must NOT inflate inDegree, or every recursive function would
    look "used" regardless of whether anything outside it calls it).
  - `OrphanRecursive -[:CALLS]-> OrphanRecursive` exists.
  - `OrphanRecursive.inDegree == 0` (self-recursion alone must not count as
    "having a caller").
  - `OrphanRecursive` DOES qualify as a Tier-3 topological-root seed
    (GraphSeedFinder.findTopologicalRootSeeds) despite calling itself —
    proving the self-loop-aware inDegree fix actually reaches seed
    detection, not just the stored property in isolation.
  - `CalledRecursive` does NOT qualify as a topological-root seed (it DOES
    have a real external caller, so inDegree must correctly read 1, not 0).
