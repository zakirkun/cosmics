package exec

import (
	"bufio"
	"io"
	"strings"
	"sync"

	"github.com/cosmics/cosmics/internal/domain"
)

// outputCollector buffers bounded output from both streams.
type outputCollector struct {
	mu       sync.Mutex
	stdout   strings.Builder
	stderr   strings.Builder
	maxBytes int
	dropped  int
}

// newOutputCollector returns a collector bounded by maxBytes per stream.
func newOutputCollector(maxBytes int) *outputCollector {
	if maxBytes <= 0 {
		maxBytes = 262144
	}
	return &outputCollector{maxBytes: maxBytes}
}

// consume reads one stream line by line, forwarding each line to the sink.
func (c *outputCollector) consume(reader io.Reader, stream Stream, sink StreamSink, clock domain.Clock) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		c.append(stream, line)

		if sink != nil {
			event := StreamEvent{Stream: stream, Line: line, At: clock.Now()}
			if err := sink(event); err != nil {
				return
			}
		}
	}
}

// append stores a line, tracking drops once the budget is spent.
func (c *outputCollector) append(stream Stream, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	target := &c.stdout
	if stream == StreamStderr {
		target = &c.stderr
	}

	if target.Len()+len(line)+1 > c.maxBytes {
		c.dropped++
		return
	}
	target.WriteString(line)
	target.WriteString("\n")
}

// result snapshots the collected output.
func (c *outputCollector) result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	stdout := c.stdout.String()
	stderr := c.stderr.String()

	return Result{
		Stdout:       stdout,
		Stderr:       stderr,
		StdoutSHA256: digestOf(stdout),
		StderrSHA256: digestOf(stderr),
		DroppedLines: c.dropped,
	}
}

// MaxOutput resolves the per-stream byte budget.
func (s RunSpec) MaxOutput() int { return 262144 }
