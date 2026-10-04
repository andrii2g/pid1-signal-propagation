package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Event struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	PID    int       `json:"pid"`
	PPID   int       `json:"ppid"`
	Name   string    `json:"event"`
	Signal string    `json:"signal,omitempty"`
	Step   int       `json:"step,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type Process struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	State   string `json:"state"`
	Command string `json:"command"`
}

type Result struct {
	Case           string    `json:"case"`
	Forward        bool      `json:"forward"`
	Container      string    `json:"container"`
	Started        time.Time `json:"started"`
	StopRequested  time.Time `json:"stop_requested"`
	StopReturned   time.Time `json:"stop_returned"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	ExitCode       int       `json:"exit_code"`
	OOMKilled      bool      `json:"oom_killed"`
	Escalation     string    `json:"escalation"`
	Work           string    `json:"work"`
	Processes      []Process `json:"processes"`
	Events         []Event   `json:"events"`
}

// Every process owns a separate file. Each event is also one stdout write.
func Emit(source, name, signal string, step int, detail string) error {
	e := Event{time.Now().UTC(), source, os.Getpid(), os.Getppid(), name, signal, step, detail}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll("/evidence", 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join("/evidence", source+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	_, err = os.Stdout.Write(b)
	return err
}

func Sort(events []Event) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
}

func Has(events []Event, source, name string) bool {
	for _, e := range events {
		if e.Source == source && e.Name == name {
			return true
		}
	}
	return false
}

func WorkOutcome(events []Event) string {
	if Has(events, "child", "job_completed") {
		return "completed"
	}
	if Has(events, "child", "job_cancelled") {
		return "cancelled"
	}
	return "completion not observed"
}
