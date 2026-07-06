package greeter

// Greeter says hello. EnglishGreeter and FormalGreeter implement it directly;
// LoudGreeter implements it via embedding EnglishGreeter (struct embedding —
// the IMPLEMENTS-via-embedding path scip-go already resolves, see RFC-001).
type Greeter interface {
	Greet(name string) string
}

type EnglishGreeter struct {
	Prefix string
}

// Greet has a value receiver.
func (g EnglishGreeter) Greet(name string) string {
	return g.Prefix + ", " + name
}

func NewEnglishGreeter() EnglishGreeter {
	return EnglishGreeter{Prefix: "Hello"}
}

// LoudGreeter embeds EnglishGreeter and inherits Greet by promotion, so it
// satisfies Greeter without redeclaring the method.
type LoudGreeter struct {
	EnglishGreeter
}

func NewLoudGreeter() LoudGreeter {
	return LoudGreeter{EnglishGreeter: EnglishGreeter{Prefix: "HELLO"}}
}
