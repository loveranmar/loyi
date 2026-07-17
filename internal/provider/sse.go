package provider

import (
	"bufio"
	"io"
	"strings"
)

// SSEData reads a text/event-stream and calls fn with each data payload.
// Returns when the stream ends or fn returns false.
func SSEData(r io.Reader, fn func(data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if !fn(data) {
			return nil
		}
	}
	return sc.Err()
}
