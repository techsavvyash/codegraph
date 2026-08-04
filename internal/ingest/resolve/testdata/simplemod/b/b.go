// Package b provides concrete types that structurally satisfy a.Storer
// through different receiver shapes: pointer receiver, value receiver,
// embedding/promotion, and a deliberate non-implementer.
package b

// FileStore implements a.Storer only via pointer receiver (*FileStore).
type FileStore struct {
	path string
}

func (f *FileStore) Save(s string) error {
	f.path = s
	return nil
}

func (f *FileStore) Load() (string, error) {
	return f.path, nil
}

// MemStore implements a.Storer via value receiver (MemStore), so both
// MemStore and *MemStore satisfy the interface.
type MemStore struct {
	data string
}

func (m MemStore) Save(s string) error {
	return nil
}

func (m MemStore) Load() (string, error) {
	return m.data, nil
}

// Nope has only half of a.Storer's method set and must NOT be reported as
// an implementer.
type Nope struct{}

func (n Nope) Save(s string) error {
	return nil
}

// Wrapped embeds FileStore, promoting Save/Load onto *Wrapped (FileStore's
// methods have pointer receivers, so Wrapped itself does not get them
// promoted as value-receiver methods — only *Wrapped does).
type Wrapped struct {
	FileStore
}
