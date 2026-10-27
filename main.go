package main

import (
	"os"

	"github.com/demetriusk/ciguse/internal/cli"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version, os.Stdout, os.Stderr))
}
