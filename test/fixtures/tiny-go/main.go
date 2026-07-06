package main

import (
	"fmt"

	"example.com/tinygo/greeter"
)

func main() {
	g := greeter.NewEnglishGreeter()
	fmt.Println(greet(g, "world"))

	formal := &greeter.FormalGreeter{Title: "Dr."}
	fmt.Println(formal.Shout("world"))

	fmt.Println(greet(greeter.NewLoudGreeter(), "world"))
}

// greet is unexported; NewEnglishGreeter/NewLoudGreeter are exported.
func greet(g greeter.Greeter, name string) string {
	return g.Greet(name)
}
