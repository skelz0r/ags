package notify

import (
	"os/exec"
	"strconv"

	"github.com/skelz0r/ags/internal/config"
)

type MacOS struct {
	Machine func() *config.Machine
}

func (n MacOS) Notify(event, title, message string) {
	if !n.Machine().Notifies(event) {
		return
	}
	script := "display notification " + strconv.Quote(message) + " with title " + strconv.Quote(title)
	exec.Command("osascript", "-e", script).Run()
}
