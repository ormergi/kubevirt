package reporter

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// filterBySinceTimestamp filters lines by matching timestamp where each line timestamp is after 'since' timestamp.
// Each line timestamp is parsed by the 'parseFn'.
func filterBySinceTimestamp(content, timestampPattern string, since time.Time, parseFn func(string) (time.Time, error)) string {
	r := regexp.MustCompile(timestampPattern)
	filtered := strings.Builder{}
	scanner := bufio.NewScanner(bytes.NewBufferString(content))
	for scanner.Scan() {
		line := scanner.Text()
		matches := r.FindStringSubmatch(line)
		if len(matches) < 1 {
			fmt.Fprintf(os.Stderr, "no matched timestamp found %q\n", matches[1])
			continue
		}
		lineTimestamp, err := parseFn(matches[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse timestamp %q: %v\n", matches[1], err)
			continue
		}

		if lineTimestamp.UTC().After(since.UTC()) {
			filtered.WriteString(line)
			filtered.WriteString("\n")
		}
	}

	return filtered.String()
}

func filterMultusLogBySinceTimestamp(content, timestampPattern string, since time.Time) string {
	return filterBySinceTimestamp(content, timestampPattern, since,
		func(s string) (time.Time, error) {
			return time.Parse(time.RFC3339, s)
		},
	)
}
