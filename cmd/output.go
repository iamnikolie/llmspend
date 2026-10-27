package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/iamnikolie/llmspend/internal/render"
)

func format() string {
	if jsonOutput {
		return "json"
	}
	if outputFormat == "" {
		return "md"
	}
	return outputFormat
}

func human() bool {
	f := format()
	return f == "md" || f == "table"
}

// writeJSON encodes v as compact UTF-8 JSON without HTML escaping.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func project(row map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := row[f]; ok {
			out[f] = v
		}
	}
	return out
}

func projectAll(rows []map[string]any, cols []string) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = project(r, cols)
	}
	return out
}

// emitRows prints rows in the active format; --fields narrows the columns.
func emitRows(rows []map[string]any, cols []string) error {
	if len(fieldsFlag) > 0 {
		cols = fieldsFlag
	}
	switch format() {
	case "json":
		if rows == nil {
			rows = []map[string]any{}
		}
		if len(fieldsFlag) > 0 {
			return writeJSON(stdout, projectAll(rows, cols))
		}
		return writeJSON(stdout, rows)
	case "csv":
		render.Separated(stdout, projectAll(rows, render.Columns(rows, cols)), render.Columns(rows, cols), ",")
	case "tsv":
		render.Separated(stdout, projectAll(rows, render.Columns(rows, cols)), render.Columns(rows, cols), "\t")
	default:
		render.Table(stdout, projectAll(rows, render.Columns(rows, cols)), render.Columns(rows, cols))
	}
	return nil
}

// footer prints a summary line under a Markdown table; machine formats
// stay pure rows.
func footer(format string, a ...any) {
	if human() {
		fmt.Fprintf(stdout, "\n"+format+"\n", a...)
	}
}

// usd rounds money for output: cents are what people read, four decimals
// keep small per-model estimates from collapsing to zero.
func usd(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func money(v float64) string {
	if v != 0 && math.Abs(v) < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}
