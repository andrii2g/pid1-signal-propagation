//go:build integration

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
)

func TestDockerMatrix(t *testing.T) {
	// The CLI expects the repository root as its build context.
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := original
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("repository root not found")
		}
		root = parent
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Error(err)
		}
	})
	base := os.Getenv("PID1LAB_RESULTS")
	if base == "" {
		base = t.TempDir()
	}
	for _, timeout := range []string{"6s", "1s"} {
		t.Run(timeout, func(t *testing.T) {
			dir := filepath.Join(base, timeout)
			if err := run([]string{"run", "--all", "--stop-timeout", timeout, "--output", dir}); err != nil {
				t.Fatal(err)
			}
			results, err := loadResults(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 8 {
				t.Fatalf("got %d experiments", len(results))
			}
			for _, r := range results {
				t.Run(r.Case+"-"+boolName(r.Forward), func(t *testing.T) { checkResult(t, r, timeout == "6s") })
			}
		})
	}
}

func boolName(b bool) string {
	if b {
		return "forward"
	}
	return "no-forward"
}

func checkResult(t *testing.T, r evidence.Result, enoughTime bool) {
	t.Helper()
	var parentPID, childPID int
	for _, e := range r.Events {
		if e.Name == "ready" {
			if e.Source == "parent" {
				parentPID = e.PID
			}
			if e.Source == "child" {
				childPID = e.PID
			}
		}
	}
	if parentPID == 0 || childPID == 0 {
		t.Fatal("missing readiness evidence")
	}
	if r.Case == "direct" || r.Case == "exec-wrapper" {
		if parentPID != 1 {
			t.Fatalf("parent PID=%d; want 1", parentPID)
		}
	}
	if r.Case == "wrapper" && parentPID == 1 {
		t.Fatal("no-exec wrapper unexpectedly replaced itself")
	}
	// Shell optimizations vary; expected behavior follows the observed tree.
	parentReceives := parentPID == 1
	if got := evidence.Has(r.Events, "parent", "signal_received"); got != parentReceives {
		t.Fatalf("parent receipt=%t, want %t", got, parentReceives)
	}
	childReceives := parentReceives && r.Forward
	if got := evidence.Has(r.Events, "child", "signal_received"); got != childReceives {
		t.Fatalf("child receipt=%t, want %t", got, childReceives)
	}
	if enoughTime && childReceives {
		if r.ExitCode != 0 || r.Work != "completed" || !evidence.Has(r.Events, "parent", "child_reaped") {
			t.Fatalf("graceful shutdown failed: %+v", r)
		}
		if r.Escalation != "no SIGKILL evidence" {
			t.Fatal(r.Escalation)
		}
	} else {
		if r.ExitCode != 137 || r.OOMKilled {
			t.Fatalf("expected forced shutdown, exit=%d oom=%t", r.ExitCode, r.OOMKilled)
		}
		if r.Escalation == "no SIGKILL evidence" {
			t.Fatal("missing termination evidence")
		}
		if !enoughTime && r.Work == "completed" {
			t.Fatal("short timeout unexpectedly allowed job completion")
		}
	}
	if len(r.Processes) < 2 {
		t.Fatal("missing process tree")
	}
	if r.StopReturned.Before(r.StopRequested) {
		t.Fatal("invalid stop timestamps")
	}
}
