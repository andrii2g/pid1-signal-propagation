package main

import (
	"testing"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
)

func TestEscalationRequiresEvidence(t *testing.T) {
	tests := []struct {
		name, events string
		exit         int
		oom          bool
		want         string
	}{
		{"daemon kill", `{"Action":"kill","Actor":{"Attributes":{"signal":"9"}}}`, 137, false, "SIGKILL observed in Docker events"},
		{"TERM only", `{"Action":"kill","Actor":{"Attributes":{"signal":"15"}}}`, 0, false, "no SIGKILL evidence"},
		{"137 ambiguous", "", 137, false, "forced termination inferred; cause unconfirmed"},
		{"OOM", "", 137, true, "OOM kill reported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyEscalation([]byte(tt.events), tt.exit, tt.oom); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
}

func TestTruncatedEvidenceIsAnError(t *testing.T) {
	if _, err := decodeEvents([]byte(`{"source":"child"`)); err == nil {
		t.Fatal("truncated evidence accepted")
	}
}

func TestMissingCompletionIsNotExplicitCancellation(t *testing.T) {
	if got := evidence.WorkOutcome([]evidence.Event{{Source: "child", Name: "job_started"}}); got != "completion not observed" {
		t.Fatal(got)
	}
}

func TestInvalidTimeoutBeforeDocker(t *testing.T) {
	if err := run([]string{"run", "--stop-timeout", "500ms"}); err == nil {
		t.Fatal("fractional timeout accepted")
	}
}
