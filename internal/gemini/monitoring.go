package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iamnikolie/llmspend/internal/client"
	"github.com/iamnikolie/llmspend/internal/spend"
)

const metricPrefix = "generativelanguage.googleapis.com/"

// metric is one Cloud Monitoring series family. Google publishes token and
// request counts only as quota usage, one metric per tier, so every tier is
// read and added. The output-token metric is the only non-quota one and the
// only one with the output modality.
type metric struct {
	kind  string // usage kind: "generate", "embeddings", "predict"
	field string // "input", "output", "requests"
	name  string // after metricPrefix
	tier  string // "1", "2", "3", "free"; "" for the output metric
}

// Metrics is every series Usage may read, per project. Token metrics are
// always read; request metrics only for the tiers a project's tokens show
// (Imagen and Veo, which have no token metric: the tiers of any tokens, or
// every tier for a project with none), which halves the calls against the
// per-user Monitoring quota.
var Metrics = []metric{
	{"generate", "output", "generate_content_usage_output_token_count", ""},
	{"generate", "input", "quota/generate_content_paid_tier_input_token_count/usage", "1"},
	{"generate", "input", "quota/generate_content_paid_tier_2_input_token_count/usage", "2"},
	{"generate", "input", "quota/generate_content_paid_tier_3_input_token_count/usage", "3"},
	{"generate", "input", "quota/generate_content_free_tier_input_token_count/usage", "free"},
	{"embeddings", "input", "quota/embed_content_paid_tier_tokens/usage", "1"},
	{"embeddings", "input", "quota/embed_content_paid_tier_2_tokens/usage", "2"},
	{"embeddings", "input", "quota/embed_content_paid_tier_3_tokens/usage", "3"},
	{"embeddings", "input", "quota/embed_content_free_tier_tokens/usage", "free"},
	{"generate", "requests", "quota/generate_requests_per_model/usage", "1"},
	{"generate", "requests", "quota/generate_content_paid_tier_2_requests/usage", "2"},
	{"generate", "requests", "quota/generate_content_paid_tier_3_requests/usage", "3"},
	{"generate", "requests", "quota/generate_content_free_tier_requests/usage", "free"},
	{"embeddings", "requests", "quota/embed_content_paid_tier_requests/usage", "1"},
	{"embeddings", "requests", "quota/embed_content_paid_tier_2_requests/usage", "2"},
	{"embeddings", "requests", "quota/embed_content_paid_tier_3_requests/usage", "3"},
	{"embeddings", "requests", "quota/embed_content_free_tier_requests/usage", "free"},
	// Imagen (predict) and Veo (predictLongRunning) are billed per image and
	// per second of video; only their request counts are published.
	{"predict", "requests", "quota/predict_requests_per_model/usage", "1"},
	{"predict", "requests", "quota/predict_requests_per_model_paid_tier_2/usage", "2"},
	{"predict", "requests", "quota/predict_requests_per_model_paid_tier_3/usage", "3"},
	{"predict", "requests", "quota/predict_requests_free_tier_per_model/usage", "free"},
	{"predict", "requests", "quota/predict_long_running_requests_per_model/usage", "1"},
	{"predict", "requests", "quota/predict_long_running_requests_per_model_paid_tier_2/usage", "2"},
	{"predict", "requests", "quota/predict_long_running_requests_per_model_paid_tier_3/usage", "3"},
	{"predict", "requests", "quota/predict_long_running_requests_per_model_free_tier/usage", "free"},
}

// parallel is how many Monitoring calls run at once.
const parallel = 8

type point struct {
	start    time.Time
	model    string
	modality string
	value    int64
}

// series reads one metric for one project, summed per model (and output
// modality) over buckets of period ending at end.
func (a *API) series(ctx context.Context, project string, m metric, from, end time.Time, period time.Duration) ([]point, error) {
	group := []string{"metric.label.model"}
	if m.field == "output" {
		group = append(group, "metric.label.output_modality")
	}
	q := url.Values{
		"filter":                         {`metric.type="` + metricPrefix + m.name + `"`},
		"interval.startTime":             {from.UTC().Format(time.RFC3339)},
		"interval.endTime":               {end.UTC().Format(time.RFC3339)},
		"aggregation.alignmentPeriod":    {strconv.FormatInt(int64(period/time.Second), 10) + "s"},
		"aggregation.perSeriesAligner":   {"ALIGN_SUM"},
		"aggregation.crossSeriesReducer": {"REDUCE_SUM"},
		"aggregation.groupByFields":      group,
	}
	var out []point
	for {
		var page struct {
			TimeSeries []struct {
				Metric struct {
					Labels map[string]string `json:"labels"`
				} `json:"metric"`
				Points []struct {
					Interval struct {
						StartTime time.Time `json:"startTime"`
					} `json:"interval"`
					Value struct {
						Int64Value  string  `json:"int64Value"`
						DoubleValue float64 `json:"doubleValue"`
					} `json:"value"`
				} `json:"points"`
			} `json:"timeSeries"`
			NextPageToken string `json:"nextPageToken"`
		}
		err := a.get(ctx, a.Monitoring, "/projects/"+url.PathEscape(project)+"/timeSeries", q, &page)
		var e *client.APIError
		if errors.As(err, &e) && e.Status == http.StatusNotFound {
			return nil, nil // metric not defined for this project: no use
		}
		if err != nil {
			return nil, err
		}
		for _, s := range page.TimeSeries {
			model := strings.TrimPrefix(s.Metric.Labels["model"], "models/")
			for _, p := range s.Points {
				v, _ := strconv.ParseInt(p.Value.Int64Value, 10, 64)
				if v == 0 {
					v = int64(p.Value.DoubleValue)
				}
				if v == 0 {
					continue
				}
				out = append(out, point{start: p.Interval.StartTime, model: model, modality: s.Metric.Labels["output_modality"], value: v})
			}
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		q.Set("pageToken", page.NextPageToken)
	}
}

// Usage reads token and request counts for every project from from to to
// (zero = now). bucket "1d" or "1h" splits by time when split is set;
// otherwise each project and model gets one row for the whole range.
// Output rows carry their modality so images, audio and video can be
// priced at their own rates. A model a project used only on the free tier
// is marked Free.
func (a *API) Usage(ctx context.Context, from, to time.Time, bucket string, split bool) ([]spend.Usage, error) {
	ids, err := a.activeIDs(ctx)
	if err != nil {
		return nil, err
	}
	step := 24 * time.Hour
	if bucket == "1h" {
		step = time.Hour
	}
	end := to
	if end.IsZero() {
		// Monitoring accepts a future end; a whole step keeps buckets on
		// UTC day (or hour) boundaries.
		end = a.Now().UTC().Truncate(step).Add(step)
	}
	period := step
	if !split {
		period = end.Sub(from).Truncate(time.Second)
		if period < time.Minute {
			period = time.Minute
		}
	}

	var (
		mu  sync.Mutex
		out []spend.Usage
	)
	// tiers[project][kind] holds the tiers with token use; "any" any kind.
	tiers := map[string]map[string]map[string]bool{}
	read := func(jobs []job) error {
		return a.each(ctx, jobs, func(j job, pts []point) {
			mu.Lock()
			defer mu.Unlock()
			for _, p := range pts {
				u := spend.Usage{Kind: j.m.kind, Model: p.model, ProjectID: j.project, Free: j.m.tier == "free", NoKeySplit: true, Start: p.start}
				if !split {
					u.Start = from
				}
				switch j.m.field {
				case "input":
					u.Input = p.value
					if tiers[j.project] == nil {
						tiers[j.project] = map[string]map[string]bool{"any": {}}
					}
					if tiers[j.project][j.m.kind] == nil {
						tiers[j.project][j.m.kind] = map[string]bool{}
					}
					tiers[j.project][j.m.kind][j.m.tier] = true
					tiers[j.project]["any"][j.m.tier] = true
				case "output":
					u.Output, u.Modality = p.value, p.modality
				case "requests":
					u.Requests = p.value
					if j.m.kind == "predict" {
						u.Quantity, u.Unit = p.value, "requests"
					}
				}
				out = append(out, u)
			}
		})
	}
	var tokens, requests []job
	for _, p := range ids {
		for _, m := range Metrics {
			if m.field != "requests" {
				tokens = append(tokens, job{p, m, from, end, period})
			}
		}
	}
	if err := read(tokens); err != nil {
		return nil, err
	}
	for _, p := range ids {
		for _, m := range Metrics {
			if m.field != "requests" {
				continue
			}
			used := tiers[p][m.kind]
			if m.kind == "predict" {
				used = tiers[p]["any"]
				if len(used) == 0 {
					used = map[string]bool{"1": true, "2": true, "3": true, "free": true}
				}
			}
			if used[m.tier] {
				requests = append(requests, job{p, m, from, end, period})
			}
		}
	}
	if err := read(requests); err != nil {
		return nil, err
	}
	markFree(out)
	return out, nil
}

// job is one series read: a metric of a project over a window.
type job struct {
	project   string
	m         metric
	from, end time.Time
	period    time.Duration
}

// each reads jobs, parallel at a time, and hands each result to got. The
// first error stops the rest.
func (a *API) each(ctx context.Context, jobs []job, got func(job, []point)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	ch := make(chan job)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				pts, err := a.series(ctx, j.project, j.m, j.from, j.end, j.period)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					mu.Unlock()
					continue
				}
				got(j, pts)
			}
		}()
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		ch <- j
	}
	close(ch)
	wg.Wait()
	return firstErr
}

// markFree flags the output and request rows of a project and model whose
// input was all free tier: the output metric has no tier label of its own.
func markFree(us []spend.Usage) {
	type pm struct{ project, kind, model string }
	paid, free := map[pm]bool{}, map[pm]bool{}
	for _, u := range us {
		if u.Input == 0 {
			continue
		}
		k := pm{u.ProjectID, u.Kind, u.Model}
		if u.Free {
			free[k] = true
		} else {
			paid[k] = true
		}
	}
	for i := range us {
		k := pm{us[i].ProjectID, us[i].Kind, us[i].Model}
		if free[k] && !paid[k] {
			us[i].Free = true
		}
	}
}
