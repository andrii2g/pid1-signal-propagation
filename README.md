# pid1-signal-propagation

A small Go lab that shows where `SIGTERM` goes when Docker stops a container.
Compare entrypoint arrangements, signal forwarding, job completion, and forced
termination using recorded events and an SVG process tree/shutdown timeline.

## Requirements

- Go 1.23 or newer to build the runner.
- Docker with a running Linux-container daemon.
- Run commands from the repository root. Initial builds download Go and Debian images.

## Run the experiments

```sh
go build -o bin/pid1lab ./cmd/pid1lab
./bin/pid1lab run --all --stop-timeout 6s --output results/baseline
```

Open `results/baseline/report.svg`. Eight experiments compare four entrypoints
with parent-to-child forwarding enabled and disabled:

| Case | Entrypoint | Expected process arrangement |
| --- | --- | --- |
| `direct` | JSON exec form | Go parent is PID 1 |
| `shell` | Shell form | Inspect the observed tree; shell behavior can vary |
| `wrapper` | Script without `exec` | Shell remains PID 1, Go parent is its child |
| `exec-wrapper` | Script ending in `exec` | Go replaces the shell as PID 1 |

The child installs its signal handler, then performs 20 steps at 250 ms per step.
The runner waits until it observes at least step 4, captures the process tree,
and requests `docker stop`. On TERM, the child drains the current job before
exiting. Without TERM, it finishes the job and remains idle, like a service
waiting for more work. Finishing a job alone therefore does not imply graceful
container shutdown. The Go parent waits for its child and records the reap.

The no-`exec` wrapper has an explicit command after launching Go to prevent a
shell from optimizing away the wrapper. The shell-form case remains unmodified;
its report reflects the actual process arrangement.

Try a deadline that cannot accommodate the remaining work:

```sh
./bin/pid1lab run --all --stop-timeout 1s --output results/short-timeout
```

Or run one case:

```sh
./bin/pid1lab run --case wrapper --forward=false --output results/wrapper
./bin/pid1lab render results/baseline --output results/baseline/regenerated.svg
```

Timeouts must be positive whole seconds. Output directories must not contain
previous results. Each run creates its own containers and removes them after
collecting evidence; images remain cached for subsequent runs.

## Evidence and interpretation

Each experiment saves `result.json`, the raw per-process `evidence/*.jsonl`,
`inspect.json`, and Docker event streams/history. Evidence is copied out before
the stopped container is removed. No host bind mount is needed for experiments.

The SVG includes a pre-stop process tree, separate Docker/parent/child lanes,
signal receipt markers, forwarding-request arrows, job progress, the configured
deadline, and the time the stop command returned. Hover over event dots for
names and times. Signal receipt is observed only when the process logs it;
issuing a forwarding request is separate evidence. Process snapshots exclude
the short-lived snapshot command itself.

The collector records host wall-clock timestamps and process timestamps in UTC.
Relative timeline positions assume host and container clocks agree. Stop latency
includes CLI overhead; the stop-return marker is not an exact process-exit time.
Job timing is repeatable but subject to scheduling and storage latency.
The timeline includes all recorded timestamps even across clock adjustments;
cross-source ordering can be unreliable if the clocks differ.

`SIGKILL` cannot be handled by a victim. A Docker kill event with signal 9 is
reported as observed SIGKILL; exit 137 without that event is only an inference,
with its cause unconfirmed. `OOMKilled` is recorded separately. Missing
`job_completed` means completion was not observed, rather than explicit job
cancellation. If PID 1 exits, Linux also kills the remaining namespace processes;
that is distinct from Docker's stop-timeout escalation.

This lab records application-level signal handling, not kernel tracing. It does
not implement a general init/subreaper or demonstrate orphan adoption. The
`child_reaped` event confirms waiting for the parent's direct child only.

## Tests

```sh
go test ./...
go vet ./...
go test -tags integration -timeout 5m -v ./cmd/pid1lab
```

Integration tests run all eight cases with both sufficient and insufficient
timeouts and assert signal receipts, job outcome, child reaping, and exit status.
Set `PID1LAB_RESULTS` to a fresh directory to retain their reports; otherwise
tests use temporary directories. Docker must be available for integration tests.

## References

- [Docker ENTRYPOINT behavior](https://docs.docker.com/reference/dockerfile/#entrypoint)
- [Docker stop signals and timeout](https://docs.docker.com/reference/cli/docker/container/stop/)
- [Linux PID namespace semantics](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html)
- [Go signal handling](https://pkg.go.dev/os/signal)
