package main

// CalledRecursive has both an external caller (main) and a self-call.
func CalledRecursive(n int) int {
	if n <= 1 {
		return 1
	}
	return n * CalledRecursive(n-1)
}

// OrphanRecursive is exported, self-recursive, and has NO external caller
// anywhere in this fixture — it must still read as a topological root
// (inDegree=0 from the graph's perspective), not as "used" merely because
// it calls itself.
func OrphanRecursive(n int) int {
	if n <= 1 {
		return 1
	}
	return n + OrphanRecursive(n-1)
}

func main() {
	CalledRecursive(5)
}
