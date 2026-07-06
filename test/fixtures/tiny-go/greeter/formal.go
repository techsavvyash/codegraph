package greeter

import "strings"

type FormalGreeter struct {
	Title string
}

// Greet has a value receiver, same as EnglishGreeter's.
func (g FormalGreeter) Greet(name string) string {
	return "Good day, " + g.Title + " " + name
}

// Shout has a pointer receiver.
func (g *FormalGreeter) Shout(name string) string {
	return strings.ToUpper(g.Greet(name))
}
