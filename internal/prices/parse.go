package prices

import (
	"regexp"
	"strconv"
	"strings"
)

// table is one Markdown table with the headings it sits under.
type table struct {
	heading string // the nearest "###" and "####" headings, joined
	header  []string
	rows    [][]string
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// tables extracts every pipe table from a Markdown page.
func tables(md string) []table {
	var out []table
	var h3, h4 string
	var cur *table
	flush := func() {
		if cur != nil && len(cur.rows) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "#### "):
			flush()
			h4 = strings.TrimPrefix(t, "#### ")
		case strings.HasPrefix(t, "### "), strings.HasPrefix(t, "## "):
			flush()
			h3 = strings.TrimLeft(t, "# ")
			h4 = ""
		case strings.HasPrefix(t, "|"):
			cells := splitRow(t)
			if cur == nil {
				cur = &table{heading: strings.TrimSpace(h3 + " " + h4), header: cells}
				continue
			}
			if isSeparator(cells) {
				continue
			}
			cur.rows = append(cur.rows, cells)
		default:
			flush()
		}
	}
	flush()
	return out
}

func col(header []string, names ...string) int {
	for _, n := range names {
		for i, h := range header {
			if strings.EqualFold(h, n) {
				return i
			}
		}
	}
	return -1
}

var dollarRe = regexp.MustCompile(`\$\s*([0-9][0-9,]*(?:\.[0-9]+)?)`)

// dollars reads "$1.25", "$10 / MTok<sup>1</sup>" or "-" (→ 0).
func dollars(s string) float64 {
	m := dollarRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	return v
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

var parenRe = regexp.MustCompile(`\s*\([^()]*(\([^()]*\)[^()]*)*\)`)

// ParseOpenAI reads the standard-tier text-token tables from OpenAI's
// pricing page. Batch, Flex, Fast and Priority sections are skipped, as are
// tables that split a model by modality or training (audio, image,
// fine-tuning). Where a model is listed with short and long context rates,
// the short-context rate is kept.
func ParseOpenAI(md string) []Price {
	var out []Price
	seen := map[string]bool{}
	for _, t := range tables(md) {
		h := strings.ToLower(t.heading)
		if strings.Contains(h, "batch") || strings.Contains(h, "flex") || strings.Contains(h, "fast") || strings.Contains(h, "priority") {
			continue
		}
		if col(t.header, "Modality", "Training", "Use case") >= 0 {
			continue
		}
		mi := col(t.header, "Model")
		in := col(t.header, "Input", "Short context input")
		outI := col(t.header, "Output", "Short context output")
		if mi != 0 || in < 0 || outI < 0 {
			continue
		}
		cached := col(t.header, "Cached input", "Short context cached input")
		write := col(t.header, "Short context 30m write", "30m write")
		for _, r := range t.rows {
			name := cell(r, mi)
			note := ""
			if strings.Contains(name, "(") {
				note = strings.Trim(strings.TrimSpace(name[strings.Index(name, "("):]), "()")
			}
			name = strings.ToLower(strings.TrimSpace(parenRe.ReplaceAllString(name, "")))
			if name == "" || seen[name] {
				continue
			}
			p := Price{
				Provider:    "openai",
				Model:       name,
				Input:       dollars(cell(r, in)),
				CachedInput: dollars(cell(r, cached)),
				CacheWrite:  dollars(cell(r, write)),
				Output:      dollars(cell(r, outI)),
				Note:        note,
			}
			if p.Input == 0 && p.Output == 0 {
				continue
			}
			seen[name] = true
			out = append(out, p)
		}
	}
	return out
}

var verRe = regexp.MustCompile(`^(\d+)(?:\.(\d+))?$`)

// anthropicIDs turns "Claude Opus 4.5" into the API model ids it prices:
// "claude-opus-4-5", plus "claude-3-5-haiku" style for the 3.x generation.
func anthropicIDs(display string) []string {
	f := strings.Fields(strings.ToLower(display))
	if len(f) < 3 || f[0] != "claude" {
		return nil
	}
	family, ver := f[1], f[2]
	m := verRe.FindStringSubmatch(ver)
	if m == nil {
		return nil
	}
	v := m[1]
	if m[2] != "" {
		v += "-" + m[2]
	}
	ids := []string{"claude-" + family + "-" + v}
	if n, _ := strconv.Atoi(m[1]); n < 4 {
		ids = append(ids, "claude-"+v+"-"+family)
	}
	return ids
}

// ParseAnthropic reads the "Model pricing" table from Anthropic's pricing
// page. When a model has more than one row (prompt-length tiers), the first
// row is kept and the tier goes into Note.
func ParseAnthropic(md string) []Price {
	var out []Price
	seen := map[string]bool{}
	for _, t := range tables(md) {
		if !strings.Contains(strings.ToLower(t.heading), "model pricing") {
			continue
		}
		mi := col(t.header, "Model")
		in := col(t.header, "Base input tokens")
		outI := col(t.header, "Output tokens")
		if mi < 0 || in < 0 || outI < 0 {
			continue
		}
		w5 := col(t.header, "5m cache writes")
		w1 := col(t.header, "1h cache writes")
		hit := col(t.header, "Cache hits and refreshes", "Cache hits")
		for _, r := range t.rows {
			raw := cell(r, mi)
			note := ""
			if i := strings.Index(raw, "("); i > 0 {
				n := strings.Trim(strings.TrimSpace(raw[i:]), "()")
				if strings.HasPrefix(n, "for ") {
					note = n
				}
			}
			name := strings.TrimSpace(parenRe.ReplaceAllString(raw, ""))
			for _, id := range anthropicIDs(name) {
				if seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, Price{
					Provider:     "anthropic",
					Model:        id,
					Input:        dollars(cell(r, in)),
					CachedInput:  dollars(cell(r, hit)),
					CacheWrite:   dollars(cell(r, w5)),
					CacheWrite1h: dollars(cell(r, w1)),
					Output:       dollars(cell(r, outI)),
					Note:         note,
				})
			}
		}
	}
	return out
}
