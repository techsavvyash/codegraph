package main

// FooStage and BarStage both declare a method named Run — the exact shape
// (multiple same-named methods on different receivers, in one file) that
// triggered the range-clobbering bug in internal/ingest/pipeline/stages.go
// on the live codegraph graph.

type FooStage struct{}

func (s *FooStage) Run() int {
	return helperA()
}

type BarStage struct{}

func (s *BarStage) Run() int {
	return helperB()
}

func helperA() int {
	return 1
}

func helperB() int {
	return 2
}

func main() {
	foo := &FooStage{}
	bar := &BarStage{}
	foo.Run()
	bar.Run()
}
