// Package a defines the Storer interface satisfied structurally by types in
// other packages of this fixture module.
package a

// Storer is a two-method interface. FileStore (pkg b, pointer receiver),
// MemStore (pkg b, value receiver), and Wrapped (pkg b, via embedding) all
// satisfy it structurally, without ever mentioning Storer.
type Storer interface {
	Save(s string) error
	Load() (string, error)
}

// Empty is an interface with no methods. Every type satisfies it, so the
// resolver must skip it entirely rather than emitting a relationship for
// every candidate in the fixture.
type Empty interface{}

// UsesError exists purely so the fixture exercises the standard library
// error interface without the resolver trying to enumerate implementations
// for it (error is a universe-scope builtin, not a project-defined type).
func UsesError() error {
	return nil
}
