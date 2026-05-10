package main

import (
	"fmt"

	"example.com/tinygo/greeter"
)

func main() {
	g := greeter.NewEnglishGreeter()
	fmt.Println(greet(g, "world"))
}

func greet(g greeter.Greeter, name string) string {
	return g.Greet(name)
}
