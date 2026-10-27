package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/skelz0r/ags/internal/config"
	"github.com/skelz0r/ags/internal/paths"
)

type Approval struct {
	Fingerprint string    `json:"fingerprint"`
	Text        string    `json:"text"`
	ApprovedAt  time.Time `json:"approved_at"`
}

type Daemon struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	LastTick  time.Time `json:"last_tick"`
}

type ConfigError struct {
	Message string    `json:"message"`
	Since   time.Time `json:"since"`
}

type State struct {
	Cursors         map[string]time.Time   `json:"cursors"`
	CursorSchedules map[string]string      `json:"cursor_schedules"`
	LastSuccess     map[string]time.Time   `json:"last_success"`
	Approvals       map[string]Approval    `json:"approvals"`
	Notified        map[string]string      `json:"notified"`
	ConfigErrors    map[string]ConfigError `json:"config_errors"`
	Daemon          Daemon                 `json:"daemon"`
	LastPrune       time.Time              `json:"last_prune"`
}

func (s *State) init() {
	if s.Cursors == nil {
		s.Cursors = map[string]time.Time{}
	}
	if s.CursorSchedules == nil {
		s.CursorSchedules = map[string]string{}
	}
	if s.LastSuccess == nil {
		s.LastSuccess = map[string]time.Time{}
	}
	if s.Approvals == nil {
		s.Approvals = map[string]Approval{}
	}
	if s.Notified == nil {
		s.Notified = map[string]string{}
	}
	if s.ConfigErrors == nil {
		s.ConfigErrors = map[string]ConfigError{}
	}
}

func (s *State) Approved(j *config.Job) bool {
	if !j.NeedsApproval() {
		return true
	}
	return s.Approvals[j.Key()].Fingerprint == j.Fingerprint
}

type Store struct {
	Paths paths.Paths
}

func Open(p paths.Paths) (*Store, error) {
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(p.StateDir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Paths: p}, nil
}

func (s *Store) Read() (*State, error) {
	st := &State{}
	data, err := os.ReadFile(s.Paths.StateFile())
	if errors.Is(err, fs.ErrNotExist) {
		st.init()
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	st.init()
	return st, nil
}

func (s *Store) Update(fn func(*State) error) error {
	lock, err := os.OpenFile(s.Paths.StateLock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	st, err := s.Read()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.Paths.StateFile(), append(data, '\n'), 0o600)
}

var syscallZero = syscall.Signal(0)
