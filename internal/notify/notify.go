package notify

import "github.com/skelz0r/ags/internal/config"

type Desktop struct {
	Machine func() *config.Machine
}

func (n Desktop) Notify(event, title, message string) {
	if !n.Machine().Notifies(event) {
		return
	}
	send(title, message)
}
