package greeter

type FormalGreeter struct {
	Title string
}

func (g FormalGreeter) Greet(name string) string {
	return "Good day, " + g.Title + " " + name
}
