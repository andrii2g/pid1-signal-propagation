package report

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/andrii2g/pid1-signal-propagation/internal/evidence"
)

func TestReportEscapesCommandsAndIsValidXML(t *testing.T) {
	now := time.Now()
	r := evidence.Result{Case: "<shell & wrapper>", Started: now.Add(-time.Second), StopRequested: now, StopReturned: now.Add(time.Second), TimeoutSeconds: 6, Processes: []evidence.Process{{PID: 1, Command: `sh -c "<bad>&"`}}}
	var b bytes.Buffer
	if err := Render(&b, []evidence.Result{r}); err != nil {
		t.Fatal(err)
	}
	d := xml.NewDecoder(bytes.NewReader(b.Bytes()))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(b.String(), "&lt;bad&gt;&amp;") {
		t.Fatal("command not escaped")
	}
}

func TestTimelineIncludesEventsAcrossClockDiscontinuity(t *testing.T) {
	now := time.Now()
	r := evidence.Result{Started: now.Add(time.Second), StopRequested: now, StopReturned: now.Add(6 * time.Second), TimeoutSeconds: 6,
		Events: []evidence.Event{{Time: now.Add(-3 * time.Second)}, {Time: now.Add(8 * time.Second)}}}
	start, end := timelineBounds(r)
	if start >= -3 || end <= 8 {
		t.Fatalf("events clipped by bounds [%f, %f]", start, end)
	}
}
