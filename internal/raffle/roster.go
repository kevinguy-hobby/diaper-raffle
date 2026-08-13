// Package raffle holds the rules of the draw: how a pasted roster turns into
// entrants, how winners are picked, and what everyone's odds look like.
//
// Nothing in here touches the database or HTTP. It is the part of the app that
// has to be right, so it is the part that is easiest to test.
package raffle

import (
	"regexp"
	"strconv"
	"strings"
)

// Entry is one guest after a roster has been parsed and de-duplicated.
type Entry struct {
	Name string `json:"name"`
	// NameKey is the case- and whitespace-insensitive identity used to merge
	// repeated lines. Two lines with the same key are the same person.
	NameKey string `json:"name_key"`
	Count   int64  `json:"diaper_count"`
	// Merged is true when more than one roster line fed this entry.
	Merged bool `json:"merged"`
}

// A trailing count is either a plain number or one written with thousands
// separators ("1,024"). The separated form is listed first so the regexp
// prefers it over matching just the final group of three digits.
var trailingCount = regexp.MustCompile(`^(.*?)[\s,;:]+(-?\d{1,3}(?:,\d{3})+(?:\.\d+)?|-?\d+(?:\.\d+)?)\s*$`)

var whitespaceRun = regexp.MustCompile(`\s+`)

// NameKey normalises a display name into its merge identity.
func NameKey(name string) string {
	return whitespaceRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), " ")
}

// ParseRoster turns pasted text into entrants, one per line.
//
// A line is "name, then diaper count": tab-separated (straight out of a
// spreadsheet), or separated by whitespace/comma/semicolon/colon. A line with
// no number on it is a guest with zero diapers, who sits the draw out.
// Repeated names are summed. Negative counts are treated as zero.
//
// Order is preserved: the first time a name appears fixes its position.
func ParseRoster(text string) []Entry {
	var order []string
	byKey := map[string]*Entry{}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		name, count := parseLine(line)
		if name == "" {
			continue
		}
		if count < 0 {
			count = 0
		}

		key := NameKey(name)
		if existing, ok := byKey[key]; ok {
			existing.Count += count
			existing.Merged = true
			continue
		}
		byKey[key] = &Entry{Name: name, NameKey: key, Count: count}
		order = append(order, key)
	}

	entries := make([]Entry, 0, len(order))
	for _, key := range order {
		entries = append(entries, *byKey[key])
	}
	return entries
}

// parseLine pulls a name and a count out of a single roster line.
func parseLine(line string) (string, int64) {
	if strings.Contains(line, "\t") {
		fields := strings.Split(line, "\t")
		name := strings.TrimSpace(fields[0])
		// Spreadsheets pad rows with empty columns and sometimes carry notes
		// alongside the number, so take the first field that parses.
		for _, field := range fields[1:] {
			if n, ok := parseCount(field); ok {
				return name, n
			}
		}
		return name, 0
	}

	if m := trailingCount.FindStringSubmatch(line); m != nil {
		name := strings.TrimRight(strings.TrimSpace(m[1]), ",;:")
		if n, ok := parseCount(m[2]); ok {
			return strings.TrimSpace(name), n
		}
	}

	return strings.TrimSpace(line), 0
}

// parseCount reads a diaper count. Diapers are whole things, so a fractional
// value is truncated rather than rejected.
func parseCount(field string) (int64, bool) {
	field = strings.TrimSpace(strings.ReplaceAll(field, ",", ""))
	if field == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(field, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// FormatRoster renders entries back out as roster text, so a roster rebuilt
// from the database looks like something a person would have typed.
func FormatRoster(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Name)
		b.WriteString(", ")
		b.WriteString(strconv.FormatInt(e.Count, 10))
		b.WriteString("\n")
	}
	return b.String()
}
