package main

import "fmt"

type Handler struct {
	Name string
}

func (h Handler) Handle(req string) string {
	return fmt.Sprintf("%s handled: %s", h.Name, req)
}

func main() {
	h := Handler{Name: "api"}
	fmt.Println(h.Handle("ping"))
}
