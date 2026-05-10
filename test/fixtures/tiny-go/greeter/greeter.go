package greeter

type Greeter interface {
	Greet(name string) string
}

type EnglishGreeter struct {
	Prefix string
}

func (g EnglishGreeter) Greet(name string) string {
	return g.Prefix + ", " + name
}

func NewEnglishGreeter() EnglishGreeter {
	return EnglishGreeter{Prefix: "Hello"}
}
