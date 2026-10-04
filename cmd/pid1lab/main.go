package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
	"github.com/andrii2g/pid1-signal-propagation/internal/report"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pid1lab run [--all] [--stop-timeout 6s] [--output results] | render DIRECTORY [--output report.svg]")
	}
	switch args[0] {
	case "render":
		if len(args) < 2 {
			return errors.New("render requires a results directory")
		}
		fs := flag.NewFlagSet("render", flag.ContinueOnError)
		output := fs.String("output", filepath.Join(args[1], "report.svg"), "SVG path")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		results, err := loadResults(args[1])
		if err != nil {
			return err
		}
		return report.Write(*output, results)
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		all := fs.Bool("all", false, "run all four entrypoints with both forwarding policies")
		which := fs.String("case", "direct", "direct, shell, wrapper, exec-wrapper")
		forward := fs.Bool("forward", true, "forward parent signals to the child")
		timeout := fs.Duration("stop-timeout", 6*time.Second, "Docker stop timeout, whole seconds")
		output := fs.String("output", "results", "artifact directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *timeout < time.Second || *timeout%time.Second != 0 {
			return errors.New("stop-timeout must be a positive whole number of seconds")
		}
		cases := []string{*which}
		policies := []bool{*forward}
		if *all {
			cases = []string{"direct", "shell", "wrapper", "exec-wrapper"}
			policies = []bool{false, true}
		}
		for _, c := range cases {
			if c != "direct" && c != "shell" && c != "wrapper" && c != "exec-wrapper" {
				return fmt.Errorf("unknown case %q", c)
			}
		}
		if err := os.MkdirAll(*output, 0755); err != nil {
			return err
		}
		// Refuse to mix old and new evidence silently.
		old, err := filepath.Glob(filepath.Join(*output, "*", "result.json"))
		if err != nil {
			return err
		}
		if len(old) > 0 {
			return errors.New("output already contains results; choose a fresh --output directory")
		}
		var results []evidence.Result
		for _, c := range cases {
			fmt.Println("Building", c)
			if _, err := docker(3*time.Minute, "build", "-f", "docker/Dockerfile", "--target", c, "-t", "pid1lab:"+c, "."); err != nil {
				return err
			}
			for _, p := range policies {
				fmt.Printf("Running %s forward=%t\n", c, p)
				r, err := experiment(c, p, int(*timeout/time.Second), *output)
				if err != nil {
					return err
				}
				results = append(results, r)
				fmt.Printf("  work=%s exit=%d escalation=%s duration=%.2fs\n", r.Work, r.ExitCode, r.Escalation, r.StopReturned.Sub(r.StopRequested).Seconds())
			}
		}
		return report.Write(filepath.Join(*output, "report.svg"), results)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func docker(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w\n%s", strings.Join(args, " "), err, b)
	}
	return b, nil
}

func save(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

func experiment(which string, forward bool, timeout int, output string) (r evidence.Result, err error) {
	dir := filepath.Join(output, fmt.Sprintf("%s-forward-%t", which, forward))
	if err = os.Mkdir(dir, 0755); err != nil {
		return r, err
	}
	r = evidence.Result{Case: which, Forward: forward, TimeoutSeconds: timeout, Started: time.Now().UTC()}
	b, err := docker(15*time.Second, "create", "-e", "FORWARD="+strconv.FormatBool(forward), "pid1lab:"+which)
	if err != nil {
		return r, err
	}
	r.Container = strings.TrimSpace(string(b))
	defer func() {
		_, cleanupErr := docker(15*time.Second, "rm", "-f", r.Container)
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	// Subscribe before start. Persist raw daemon events independently of container output.
	eventFile, err := os.Create(filepath.Join(dir, "docker-events.jsonl"))
	if err != nil {
		return r, err
	}
	eventCtx, eventCancel := context.WithCancel(context.Background())
	eventCmd := exec.CommandContext(eventCtx, "docker", "events", "--since", r.Started.Format(time.RFC3339Nano), "--filter", "container="+r.Container, "--format", "{{json .}}")
	eventCmd.Stdout = eventFile
	var eventErrors strings.Builder
	eventCmd.Stderr = &eventErrors
	if err = eventCmd.Start(); err != nil {
		eventCancel()
		eventFile.Close()
		return r, err
	}
	defer func() { eventCancel(); eventCmd.Wait(); eventFile.Close() }()
	if _, err = docker(15*time.Second, "start", r.Container); err != nil {
		return r, err
	}
	deadline := time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		logs, logErr := docker(5*time.Second, "logs", r.Container)
		if logErr != nil {
			return r, logErr
		}
		for _, e := range parseEvents(logs) {
			if e.Source == "child" && e.Name == "job_progress" && e.Step >= 4 {
				ready = true
			}
		}
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		return r, errors.New("child did not reach step 4 before readiness deadline")
	}
	b, err = docker(10*time.Second, "exec", r.Container, "/probe", "snapshot")
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r.Processes); err != nil {
		return r, err
	}
	r.StopRequested = time.Now().UTC()
	if _, err = docker(time.Duration(timeout+15)*time.Second, "stop", "--timeout", strconv.Itoa(timeout), r.Container); err != nil {
		return r, err
	}
	r.StopReturned = time.Now().UTC()
	b, err = docker(10*time.Second, "inspect", r.Container)
	if err != nil {
		return r, err
	}
	if err = os.WriteFile(filepath.Join(dir, "inspect.json"), b, 0644); err != nil {
		return r, err
	}
	var inspected []struct {
		State struct {
			ExitCode  int
			OOMKilled bool
		}
	}
	if err = json.Unmarshal(b, &inspected); err != nil {
		return r, err
	}
	if len(inspected) != 1 {
		return r, errors.New("unexpected inspect response")
	}
	r.ExitCode, r.OOMKilled = inspected[0].State.ExitCode, inspected[0].State.OOMKilled
	if _, err = docker(15*time.Second, "cp", r.Container+":/evidence", dir); err != nil {
		return r, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "evidence", "*.jsonl"))
	if err != nil {
		return r, err
	}
	for _, f := range files {
		b, err = os.ReadFile(f)
		if err != nil {
			return r, err
		}
		events, parseErr := decodeEvents(b)
		if parseErr != nil {
			return r, fmt.Errorf("%s: %w", f, parseErr)
		}
		r.Events = append(r.Events, events...)
	}
	evidence.Sort(r.Events)
	r.Work = evidence.WorkOutcome(r.Events)
	// Query the bounded daemon history too: a streaming event subscriber can lag.
	b, err = docker(10*time.Second, "events", "--since", r.Started.Format(time.RFC3339Nano), "--until", time.Now().UTC().Format(time.RFC3339Nano), "--filter", "container="+r.Container, "--format", "{{json .}}")
	if err != nil {
		return r, err
	}
	if err = os.WriteFile(filepath.Join(dir, "docker-events-history.jsonl"), b, 0644); err != nil {
		return r, err
	}
	r.Escalation = classifyEscalation(b, r.ExitCode, r.OOMKilled)
	if err = save(filepath.Join(dir, "result.json"), r); err != nil {
		return r, err
	}
	return r, nil
}

func parseEvents(b []byte) []evidence.Event {
	var events []evidence.Event
	for _, line := range strings.Split(string(b), "\n") {
		var e evidence.Event
		if json.Unmarshal([]byte(line), &e) == nil && e.Source != "" {
			events = append(events, e)
		}
	}
	return events
}

func decodeEvents(b []byte) ([]evidence.Event, error) {
	var events []evidence.Event
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e evidence.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, err
		}
		if e.Source == "" || e.Time.IsZero() {
			return nil, errors.New("event missing source or timestamp")
		}
		events = append(events, e)
	}
	return events, nil
}

func classifyEscalation(b []byte, exit int, oom bool) string {
	for _, line := range strings.Split(string(b), "\n") {
		var e struct {
			Action string
			Actor  struct{ Attributes map[string]string }
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Action == "kill" {
			s := e.Actor.Attributes["signal"]
			if s == "9" || s == "SIGKILL" {
				return "SIGKILL observed in Docker events"
			}
		}
	}
	if oom {
		return "OOM kill reported"
	}
	if exit == 137 {
		return "forced termination inferred; cause unconfirmed"
	}
	return "no SIGKILL evidence"
}

func loadResults(dir string) ([]evidence.Result, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "result.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no result.json files found")
	}
	var results []evidence.Result
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var r evidence.Result
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}
