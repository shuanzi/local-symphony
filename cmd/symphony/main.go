package main

import (
	"os"

	"local-symphony/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
