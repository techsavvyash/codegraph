package main

// Reader/ReadWriter cover embedded-interface IMPLEMENTS: ReadWriter embeds
// Reader, and a concrete type (Buffer) implementing both Read and Write must
// produce an IMPLEMENTS edge from Buffer.Read to the embedded Reader.Read
// member (not just to ReadWriter's own method set).
type Reader interface {
	Read() string
}

type ReadWriter interface {
	Reader
	Write(s string)
}

type Buffer struct {
	contents string
}

func (b *Buffer) Read() string {
	return b.contents
}

func (b *Buffer) Write(s string) {
	b.contents = s
}
