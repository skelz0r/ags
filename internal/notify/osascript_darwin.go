package notify

import (
	"os/exec"
	"strconv"
)

func send(title, message string) {
	script := "display notification " + strconv.Quote(message) + " with title " + strconv.Quote(title)
	exec.Command("osascript", "-e", script).Run()
}
