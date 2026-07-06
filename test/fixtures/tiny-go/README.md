# tiny-go

Minimal Go module exercising the graph shapes correctness/idempotency tests care
about: an interface with two direct implementations plus one via struct
embedding, cross-package calls, value- and pointer-receiver methods, and an
exported/unexported function pair. Used by `test/harness/golden_test.go`
(`TestGoldenTinyGo`), `test/harness/lsp_test.go` (`TestQueryTinyGo`), and the
RFC-006 Phase 1 idempotency test (index twice, assert identical counts).
