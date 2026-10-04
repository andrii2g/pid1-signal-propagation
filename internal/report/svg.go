package report

import (
	"fmt"
	"html"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
)

func Write(path string, results []evidence.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = Render(f, results)
	returnErr := f.Close()
	if err != nil {
		return err
	}
	return returnErr
}

func Render(w io.Writer, results []evidence.Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="1400" height="%d" viewBox="0 0 1400 %d"><style>text{font-family:monospace;fill:#dde7f4;font-size:13px}.title{font-size:24px;font-weight:bold}.heading{font-size:18px;fill:#fff}.muted{fill:#9aacc3}.good{fill:#63dda2}.bad{fill:#ffab76}</style><rect width="100%%" height="100%%" fill="#101827"/>`, 120+len(results)*360, 120+len(results)*360)
	text(&b, 28, 40, "PID 1 / container shutdown lab", "title")
	text(&b, 28, 68, "Solid markers = observations. Dashed deadline = configured grace period. Process tree = pre-stop snapshot.", "muted")
	text(&b, 28, 92, "Signal arrows show forwarding requests; child receipt is a separate marker. Times are relative to the host stop request.", "muted")
	for i, r := range results {
		y := 125 + i*360
		fmt.Fprintf(&b, `<rect x="16" y="%d" width="1368" height="342" rx="8" fill="#192438"/>`, y)
		text(&b, 32, y+28, fmt.Sprintf("%s   forward=%t   exit=%d   stop=%.2fs", r.Case, r.Forward, r.ExitCode, r.StopReturned.Sub(r.StopRequested).Seconds()), "heading")
		text(&b, 32, y+52, "Work: "+r.Work+" | "+r.Escalation, "muted")
		tree(&b, r.Processes, 32, y+80)
		timeline(&b, r, 450, y+92)
		parentSig := evidence.Has(r.Events, "parent", "signal_received")
		childSig := evidence.Has(r.Events, "child", "signal_received")
		text(&b, 32, y+305, fmt.Sprintf("TERM receipt: parent=%t child=%t | child reaped=%t", parentSig, childSig, evidence.Has(r.Events, "parent", "child_reaped")), "muted")
	}
	b.WriteString("</svg>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func text(w io.Writer, x, y int, s, class string) {
	fmt.Fprintf(w, `<text x="%d" y="%d" class="%s">%s</text>`, x, y, class, html.EscapeString(s))
}

func tree(w io.Writer, ps []evidence.Process, x, y int) {
	ps = append([]evidence.Process(nil), ps...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].PID < ps[j].PID })
	rows := map[int]int{}
	depths := map[int]int{}
	for i, p := range ps {
		rows[p.PID] = y + 25 + i*46
	}
	for _, p := range ps {
		depth := 0
		parent := p.PPID
		seen := map[int]bool{p.PID: true}
		for {
			found := false
			for _, q := range ps {
				if q.PID == parent && !seen[parent] {
					seen[parent] = true
					depth++
					parent = q.PPID
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		depths[p.PID] = depth
	}
	text(w, x, y, "PROCESS TREE (container PIDs)", "muted")
	for _, p := range ps {
		px, py := x+depths[p.PID]*22, rows[p.PID]
		if parentY, ok := rows[p.PPID]; ok {
			fmt.Fprintf(w, `<path d="M%d %d V%d H%d" stroke="#748ba7" fill="none"/>`, x+depths[p.PPID]*22+5, parentY+4, py-5, px-3)
		}
		cmd := p.Command
		if len(cmd) > 43 {
			cmd = cmd[:40] + "..."
		}
		text(w, px, py, fmt.Sprintf("PID %d [%s] %s", p.PID, p.State, cmd), "")
	}
}

func timeline(w io.Writer, r evidence.Result, x, y int) {
	start, end := timelineBounds(r)
	pos := func(t time.Time) int { return x + 95 + int((t.Sub(r.StopRequested).Seconds()-start)/(end-start)*740) }
	zero := pos(r.StopRequested)
	deadline := pos(r.StopRequested.Add(time.Duration(r.TimeoutSeconds) * time.Second))
	for i, lane := range []string{"Docker", "parent", "child"} {
		ly := y + i*54
		text(w, x, ly+4, lane, "")
		fmt.Fprintf(w, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#53657e"/>`, x+95, ly, x+835, ly)
	}
	fmt.Fprintf(w, `<path d="M%d %d V%d" stroke="#70c9ff"/><path d="M%d %d V%d" stroke="#ffab76" stroke-dasharray="5 4"/>`, zero, y-15, y+120, deadline, y-15, y+120)
	text(w, zero-20, y-25, "stop 0s", "")
	text(w, deadline-55, y+145, fmt.Sprintf("deadline +%ds", r.TimeoutSeconds), "muted")
	text(w, x+95, y+173, fmt.Sprintf("window %.1fs to +%.1fs", start, end), "muted")
	for _, e := range r.Events {
		ly := y + 54
		if e.Source == "child" {
			ly = y + 108
		}
		color := "#90a3bb"
		label := ""
		switch e.Name {
		case "signal_received":
			color = "#70c9ff"
			label = "TERM received"
		case "signal_forward_requested":
			fmt.Fprintf(w, `<path d="M%d %d V%d l-4 -6 m4 6 l4 -6" fill="none" stroke="#70c9ff"/>`, pos(e.Time), y+58, y+99)
		case "job_completed":
			color = "#63dda2"
			label = "completed"
		case "exit":
			color = "#63dda2"
			label = "exit"
		case "job_progress":
			color = "#cfb4ff"
		case "ready", "job_started", "child_started", "child_reaped", "drain_started":
			continue
		}
		fmt.Fprintf(w, `<circle cx="%d" cy="%d" r="4" fill="%s"><title>%s at %+.3fs</title></circle>`, pos(e.Time), ly, color, html.EscapeString(e.Source+": "+e.Name), e.Time.Sub(r.StopRequested).Seconds())
		if label != "" {
			offset := -12
			if e.Name == "exit" {
				offset = 22
			}
			text(w, pos(e.Time)-20, ly+offset, label, "")
		}
	}
	finish := pos(r.StopReturned)
	fmt.Fprintf(w, `<circle cx="%d" cy="%d" r="5" fill="#ffab76"><title>docker stop returned; this timestamp is not exact process exit time</title></circle>`, finish, y)
	text(w, finish-50, y+24, "stop returned", "")
}

// Include every recorded timestamp, even if clocks drift or step during a run.
func timelineBounds(r evidence.Result) (float64, float64) {
	start, end := 0.0, float64(r.TimeoutSeconds)
	include := func(t time.Time) {
		v := t.Sub(r.StopRequested).Seconds()
		if v < start {
			start = v
		}
		if v > end {
			end = v
		}
	}
	include(r.Started)
	include(r.StopReturned)
	for _, e := range r.Events {
		include(e.Time)
	}
	return start - 0.25, end + 0.5
}
