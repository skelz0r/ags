package main

import (
	"os"

	"github.com/skelz0r/ags/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
