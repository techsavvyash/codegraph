package main

import "fmt"

// main wires up enough real usage that go build/go vet see every construct
// as reachable; the indexing tests don't depend on runtime behavior, only on
// static structure, so this is intentionally minimal.
func main() {
	mem := MemStore{}
	file := &FileStore{}
	_ = persist(mem, "hello")
	_ = persist(file, "hello")

	var buf Buffer
	var rw ReadWriter = &buf
	rw.Write("x")
	fmt.Println(rw.Read())

	fmt.Println(a())
	fmt.Println(useSum())
}
