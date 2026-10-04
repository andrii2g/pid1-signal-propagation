//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
)

func emit(source, name, sig string, step int, detail string) {
	if err := evidence.Emit(source, name, sig, step, detail); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: probe parent|child|snapshot")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "parent":
		parent()
	case "child":
		child()
	case "snapshot":
		if err := snapshot(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	default:
		os.Exit(2)
	}
}

func notifications() chan os.Signal {
	c := make(chan os.Signal, 8)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	return c
}

func parent() {
	sigs := notifications()
	defer signal.Stop(sigs)
	emit("parent", "ready", "", 0, "")
	cmd := exec.Command("/probe", "child")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		emit("parent", "error", "", 0, err.Error())
		os.Exit(2)
	}
	emit("parent", "child_started", "", 0, strconv.Itoa(cmd.Process.Pid))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-sigs:
			emit("parent", "signal_received", sig.String(), 0, "")
			if os.Getenv("FORWARD") == "true" {
				err := cmd.Process.Signal(sig)
				detail := "sent to child"
				if err != nil {
					detail = err.Error()
				}
				emit("parent", "signal_forward_requested", sig.String(), 0, detail)
			}
		case err := <-done:
			detail := "exit 0"
			if err != nil {
				detail = err.Error()
			}
			emit("parent", "child_reaped", "", 0, detail)
			emit("parent", "exit", "", 0, detail)
			if err != nil {
				os.Exit(1)
			}
			return
		}
	}
}

func child() {
	sigs := notifications()
	defer signal.Stop(sigs)
	emit("child", "ready", "", 0, "")
	emit("child", "job_started", "", 0, "")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	step, draining := 0, false
	for {
		select {
		case sig := <-sigs:
			emit("child", "signal_received", sig.String(), step, "")
			draining = true
			emit("child", "drain_started", "", step, "")
			if step == 20 {
				emit("child", "exit", "", step, "")
				return
			}
		case <-ticker.C:
			if step == 20 {
				continue
			}
			step++
			emit("child", "job_progress", "", step, "")
			if step == 20 {
				emit("child", "job_completed", "", step, "")
				if draining {
					emit("child", "exit", "", step, "")
					return
				}
			}
		}
	}
}

func snapshot() error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	var processes []evidence.Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// comm may contain spaces or parentheses; fields after its final ')' are fixed.
		end := strings.LastIndexByte(string(b), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cmd, _ := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		processes = append(processes, evidence.Process{PID: pid, PPID: ppid, State: fields[0], Command: strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))})
	}
	return json.NewEncoder(os.Stdout).Encode(processes)
}
