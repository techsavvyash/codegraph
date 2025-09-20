package main

import "fmt"

// SimpleFunction demonstrates basic functionality
func SimpleFunction() {
	fmt.Println("Hello from simple function")
}

// AnotherFunction with some complexity
func AnotherFunction(x int, y string) (int, error) {
	if x < 0 {
		return 0, fmt.Errorf("negative value: %d", x)
	}

	result := x * 2
	fmt.Printf("Processing: %s with value %d, result: %d\n", y, x, result)
	return result, nil
}

func main() {
	SimpleFunction()

	value, err := AnotherFunction(42, "test")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Final result: %d\n", value)
}