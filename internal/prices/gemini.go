package prices

import (
	"regexp"
	"strings"
)

var (
	geminiIDRe     = regexp.MustCompile("`([a-z0-9][a-z0-9.-]*)`")
	geminiAmountRe = regexp.MustCompile(`\$\s*([0-9][0-9,]*(?:\.[0-9]+)?)`)
	geminiUntilRe  = regexp.MustCompile(`through ([A-Z][a-z]+ \d{1,2}, \d{4})`)
	geminiParenRe  = regexp.MustCompile(`\(([^()]*)\)`)
	geminiRouteRe  = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|.*routes API requests to `([^`]+)`")
)

// geminiRate is one dollar amount in a pricing cell and what follows it.
type geminiRate struct {
	usd      float64
	modality string // "text", "image", "audio", "video" or "" (unlabelled)
	perUnit  bool   // "$0.039 per image", "$0.005/min": not a per-token rate
}

// geminiRates reads a Paid Tier cell: "$0.75 through December 31, 2026.
// $1.50 starting January 1, 2027." keeps the current rate only; "$3 (text
// and thinking) $60.00 (images) Equivalent to …" gives one rate per
// modality; "$3.00 or $0.005/min (audio)" labels $3.00 as audio.
func geminiRates(s string) []geminiRate {
	if i := strings.Index(s, "Equivalent"); i >= 0 {
		s = s[:i]
	}
	locs := geminiAmountRe.FindAllStringSubmatchIndex(s, -1)
	var out []geminiRate
	for i, l := range locs {
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		tail := strings.TrimSpace(s[l[1]:end])
		if strings.Contains(tail, "starting ") {
			continue // a future rate
		}
		r := geminiRate{usd: dollars(s[l[0]:l[1]])}
		lt := strings.ToLower(tail)
		r.perUnit = strings.HasPrefix(lt, "/") || strings.HasPrefix(lt, "per ")
		label := ""
		if (lt == "or" || strings.HasPrefix(lt, "or ")) && i+1 < len(locs) {
			next := len(s)
			if i+2 < len(locs) {
				next = locs[i+2][0]
			}
			lt = strings.ToLower(s[locs[i+1][1]:next])
		}
		if m := geminiParenRe.FindStringSubmatch(lt); m != nil {
			label = m[1]
		}
		switch {
		case strings.Contains(label, "text"):
			r.modality = "text"
		case strings.Contains(label, "image"):
			r.modality = "image"
		case strings.Contains(label, "audio"):
			r.modality = "audio"
		case strings.Contains(label, "video"):
			r.modality = "video"
		}
		out = append(out, r)
	}
	return out
}

// firstTokenRate is the first per-token rate in a cell (text before audio,
// short prompts before long ones).
func firstTokenRate(s string) float64 {
	for _, r := range geminiRates(s) {
		if !r.perUnit {
			return r.usd
		}
	}
	return 0
}

// ParseGemini reads the Standard tables of the Gemini API pricing page: per
// model the paid-tier input, context-caching and output rates, output split
// by modality where the page does (image, audio, video), and the per-song
// price of the music models. When a rate changes on a date, the current one
// is kept and the date goes into Note. Models priced only per image, per
// second or per minute are left out: their usage cannot be priced from
// token counts.
func ParseGemini(md string) []Price {
	var out []Price
	seen := map[string]bool{}
	var ids []string
	sub := ""
	used := false // one table per model section
	var header []string
	var rows [][]string
	flush := func() {
		defer func() { header, rows = nil, nil }()
		if len(rows) == 0 || len(ids) == 0 || used || (sub != "" && sub != "Standard") {
			return
		}
		used = true
		paid := -1
		for i, h := range header {
			if strings.Contains(strings.ToLower(h), "paid") {
				paid = i
			}
		}
		if paid < 0 {
			return
		}
		if strings.Contains(strings.ToLower(header[paid]), "per request") {
			for _, r := range rows {
				id := strings.ToLower(strings.TrimSpace(parenRe.ReplaceAllString(cell(r, 0), "")))
				id = strings.Join(strings.Fields(id), "-")
				rates := geminiRates(cell(r, paid))
				if len(rates) == 0 || seen[id] || !contains(ids, id) {
					continue
				}
				note := ""
				if i := strings.Index(cell(r, paid), " per "); i >= 0 {
					note = strings.TrimSpace(cell(r, paid)[i:])
				}
				seen[id] = true
				out = append(out, Price{Provider: "gemini", Model: id, PerRequest: rates[0].usd, Note: note})
			}
			return
		}
		var p Price
		var until string
		var first float64
		perUnitOnly := false
		for _, r := range rows {
			label := strings.ToLower(cell(r, 0))
			v := cell(r, paid)
			if m := geminiUntilRe.FindStringSubmatch(v); m != nil && until == "" {
				until = m[1]
			}
			switch {
			case strings.HasPrefix(label, "input price"), label == "text input price":
				p.Input = firstTokenRate(v)
			case strings.HasPrefix(label, "context caching price"):
				p.CachedInput = firstTokenRate(v)
			case strings.HasPrefix(label, "output price"):
				perUnitOnly = true
				for _, rt := range geminiRates(v) {
					if rt.perUnit {
						continue
					}
					perUnitOnly = false
					switch rt.modality {
					case "image":
						p.OutputImage = rt.usd
					case "audio":
						p.OutputAudio = rt.usd
					case "video":
						p.OutputVideo = rt.usd
					default:
						if p.Output == 0 {
							p.Output = rt.usd
						}
					}
					if first == 0 {
						first = rt.usd
					}
				}
			}
		}
		if p.Output == 0 {
			p.Output = first // speech models: audio is the only output
		}
		if perUnitOnly {
			return // "$0.039 per image": not a token rate
		}
		if p.Input == 0 && p.Output == 0 {
			return
		}
		if until != "" {
			p.Note = "until " + until
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			q := p
			q.Provider, q.Model = "gemini", id
			out = append(out, q)
		}
	}
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "## "):
			flush()
			ids, sub, used = nil, "", false
		case strings.HasPrefix(t, "### "):
			flush()
			sub = strings.TrimSpace(strings.TrimPrefix(t, "### "))
		case strings.HasPrefix(t, "*[`"):
			ids = nil
			for _, m := range geminiIDRe.FindAllStringSubmatch(t, -1) {
				ids = append(ids, m[1])
			}
		case strings.HasPrefix(t, "|"):
			cells := splitRow(t)
			if header == nil {
				header = cells
			} else if !isSeparator(cells) {
				rows = append(rows, cells)
			}
		default:
			if header != nil {
				flush()
			}
		}
	}
	flush()
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// GeminiRouted adds a price for every deprecated model id the API routes to
// a newer model (gemini-3.7-flash → gemini-3.8-flash), copied from the
// target's price, when the pricing page does not list the old id itself.
func GeminiRouted(ps []Price, deprecationsMD string) []Price {
	have := map[string]Price{}
	for _, p := range ps {
		have[p.Model] = p
	}
	for _, line := range strings.Split(deprecationsMD, "\n") {
		m := geminiRouteRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		from, to := m[1], m[2]
		target, ok := have[to]
		if _, dup := have[from]; dup || !ok {
			continue
		}
		q := target
		q.Model = from
		q.Note = strings.TrimSpace("routed to " + to + "; " + target.Note)
		q.Note = strings.TrimSuffix(q.Note, ";")
		have[from] = q
		ps = append(ps, q)
	}
	return ps
}
