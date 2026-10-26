package notify

import "os/exec"

func send(title, message string) {
	program, err := exec.LookPath("notify-send")
	if err != nil {
		return
	}
	exec.Command(program, "--app-name=ags", "--", title, message).Run()
}
