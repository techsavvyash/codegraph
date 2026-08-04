package main

// Storer is implemented by both a value-receiver type (MemStore) and a
// pointer-receiver type (FileStore). persist() calls through the interface,
// so the pipeline must fan the CALLS edge out to both concrete Save methods
// (RFC-001 Layers 1+2 interface dispatch).
type Storer interface {
	Save(data string) error
}

type MemStore struct {
	data string
}

// Save has a value receiver.
func (m MemStore) Save(data string) error {
	m.data = data
	return nil
}

type FileStore struct {
	path string
}

// Save has a pointer receiver.
func (f *FileStore) Save(data string) error {
	f.path = data
	return nil
}

// persist calls Save through the Storer interface — both MemStore.Save and
// FileStore.Save are call-graph fan-out targets.
func persist(s Storer, data string) error {
	return s.Save(data)
}
