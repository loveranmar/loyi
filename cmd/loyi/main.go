package main

import (
	"fmt"
	"os"
)

const version = "0.0.1-dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("loyi " + version)
			return
		}
	}
	fmt.Println("loyi — your agentic cli, for people who actually ship.")
	fmt.Println("nothing to run yet; this is a scaffold.")
}
