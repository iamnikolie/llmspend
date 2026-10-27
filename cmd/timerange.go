package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// now is swapped in tests.
var now = func() time.Time { return time.Now().UTC() }

var relRe = regexp.MustCompile(`^(\d+)([dhw])$`)

// parseSince reads "7d", "24h", "2w", "2026-10-01" or RFC 3339. Relative
// values snap to bucket starts so "7d" means today plus the six days before
// it and "24h" the current hour plus the 23 before it.
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if m := relRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n < 1 {
			return time.Time{}, fmt.Errorf("--since %q: must be at least 1", s)
		}
		t := now()
		switch m[2] {
		case "h":
			return t.Truncate(time.Hour).Add(-time.Duration(n-1) * time.Hour), nil
		case "w":
			n *= 7
		}
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return d.AddDate(0, 0, -(n - 1)), nil
	}
	return parseTime(s)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("cannot read time %q: use 7d, 24h, 2w, YYYY-MM-DD or RFC 3339", s)
}

// parseUntil reads an end time; a bare date means the end of that day.
func parseUntil(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().AddDate(0, 0, 1), nil
	}
	return parseTime(s)
}

// monthRange reads "" or "current" (this month to now), "last"/"previous",
// or YYYY-MM. The end is exclusive; for the current month it is zero (now).
func monthRange(s string) (time.Time, time.Time, string, error) {
	t := now()
	cur := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "current", "this":
		return cur, time.Time{}, cur.Format("2006-01") + " (month to date)", nil
	case "last", "previous", "prev":
		from := cur.AddDate(0, -1, 0)
		return from, cur, from.Format("2006-01"), nil
	}
	from, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("--month %q: use YYYY-MM, current or last", s)
	}
	to := from.AddDate(0, 1, 0)
	label := from.Format("2006-01")
	if !to.Before(t) {
		return from, time.Time{}, label + " (month to date)", nil
	}
	return from, to, label, nil
}
