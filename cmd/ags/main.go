package main

import (
	"os"

	"github.com/skelz0r/ags/internal/cli"
	"github.com/skelz0r/ags/internal/tui"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], tui.Run))
}
