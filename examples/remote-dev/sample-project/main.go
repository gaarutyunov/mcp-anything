package main

import (
	"fmt"
	"os"
)

func main() {
	os.Remove("/tmp/mcp-test-cleanup") // errcheck: result of os.Remove should be checked
	fmt.Println("Hello, workspace!")
}
