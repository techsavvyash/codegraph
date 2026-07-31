package main

// a calls b; b does not call a. Direction sanity fixture.
func a() int {
	return b() + 1
}

func b() int {
	return 1
}
