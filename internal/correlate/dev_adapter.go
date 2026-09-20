package correlate

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"traceiq/internal/model"
)

// FileLogAdapter is the DR-20 §20.5 first-class "file" driver: NDJSON log
// lines under a per-tenant subdirectory, zero external services. It is the
// dev-profile default. Line shape: {ts, trace_id, span_id, service, level,
// body, attrs}.
type FileLogAdapter struct {
	root string
}

// NewFileLogAdapter constructs a FileLogAdapter rooted at dir (per DR-20
// §20.5, correlate.logs.path). Per-tenant data lives at dir/<tenant>/*.ndjson.
func NewFileLogAdapter(dir string) *FileLogAdapter {
	return &FileLogAdapter{root: dir}
}

func (a *FileLogAdapter) Name() string { return "file" }

type ndjsonLogLine struct {
	TS      time.Time         `json:"ts"`
	TraceID string            `json:"trace_id"`
	SpanID  string            `json:"span_id"`
	Service string            `json:"service"`
	Level   string            `json:"level"`
	Body    string            `json:"body"`
	Attrs   map[string]string `json:"attrs"`
}

func decodeTraceID(s string) (model.TraceID, bool) {
	var tid model.TraceID
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(tid) {
		return tid, false
	}
	copy(tid[:], b)
	return tid, true
}

func decodeSpanID(s string) (model.SpanID, bool) {
	var sid model.SpanID
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(sid) {
		return sid, false
	}
	copy(sid[:], b)
	return sid, true
}

func attrsToMap(m map[string]string) model.AttrMap {
	if len(m) == 0 {
		return nil
	}
	out := make(model.AttrMap, len(m))
	for k, v := range m {
		out[k] = model.AttrValue{Kind: model.AttrStr, Str: v}
	}
	return out
}

// readTenantLines reads every NDJSON line for tenant tid, tolerating a
// missing directory (returns no lines, no error — an empty dev fixture).
func (a *FileLogAdapter) readTenantLines(tid model.TenantID) ([]ndjsonLogLine, error) {
	dir := filepath.Join(a.root, string(tid))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ndjsonLogLine
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var l ndjsonLogLine
			if err := json.Unmarshal([]byte(line), &l); err != nil {
				continue // dev adapter: skip malformed lines rather than fail the whole read
			}
			out = append(out, l)
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func inWindow(ts time.Time, w model.Window) bool {
	if !w.Start.IsZero() && ts.Before(w.Start) {
		return false
	}
	if !w.End.IsZero() && ts.After(w.End) {
		return false
	}
	return true
}

// LogsForTrace implements LogAdapter (DR-20 §20.1, FR-F07-1): exact
// trace_id-indexed join, per-tenant subdirectory.
func (a *FileLogAdapter) LogsForTrace(ctx context.Context, tid model.TenantID, traceID model.TraceID, w model.Window, limit int) ([]model.LogLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lines, err := a.readTenantLines(tid)
	if err != nil {
		return nil, err
	}
	want := hex.EncodeToString(traceID[:])
	var out []model.LogLine
	for _, l := range lines {
		if l.TraceID != want || !inWindow(l.TS, w) {
			continue
		}
		tidB, ok := decodeTraceID(l.TraceID)
		if !ok {
			continue
		}
		sidB, _ := decodeSpanID(l.SpanID)
		out = append(out, model.LogLine{
			Timestamp: l.TS,
			TraceID:   tidB,
			SpanID:    sidB,
			Service:   l.Service,
			Level:     l.Level,
			Body:      l.Body,
			Attrs:     attrsToMap(l.Attrs),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// QueryByServiceWindow implements LogAdapter's heuristic fallback
// (FR-F07-5): contains is matched as a literal substring, server-side,
// after retrieval — never interpolated into a query language.
func (a *FileLogAdapter) QueryByServiceWindow(ctx context.Context, tid model.TenantID, service string, w model.Window, contains string, limit int) ([]model.LogLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lines, err := a.readTenantLines(tid)
	if err != nil {
		return nil, err
	}
	var out []model.LogLine
	for _, l := range lines {
		if l.Service != service || !inWindow(l.TS, w) {
			continue
		}
		if contains != "" && !strings.Contains(l.Body, contains) {
			continue
		}
		tidB, _ := decodeTraceID(l.TraceID)
		sidB, _ := decodeSpanID(l.SpanID)
		out = append(out, model.LogLine{
			Timestamp: l.TS,
			TraceID:   tidB,
			SpanID:    sidB,
			Service:   l.Service,
			Level:     l.Level,
			Body:      l.Body,
			Attrs:     attrsToMap(l.Attrs),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a *FileLogAdapter) Capabilities() LogCapabilities {
	return LogCapabilities{TenantScoped: true, TraceIDIndexed: true, MaxLookback: 0}
}

func (a *FileLogAdapter) Health(ctx context.Context) model.HealthReport {
	return model.HealthReport{Healthy: true, Message: "file adapter", CheckedAt: time.Now().UTC()}
}

func (a *FileLogAdapter) Close() error { return nil }

// FileMetricAdapter is the dev-profile MetricAdapter: exemplars and range
// samples are read from per-tenant NDJSON snapshot files under dir, rather
// than the full Prometheus text-format `.prom` snapshot format the spec
// describes for the production file driver (DR-20 §20.5) — a scoped-down
// substitute so tests exercise the join with zero external services and
// zero third-party Prometheus text-format parsing code. See
// docs/reports/w11-correlate-memory.md for this deviation.
type FileMetricAdapter struct {
	root string
}

func NewFileMetricAdapter(dir string) *FileMetricAdapter {
	return &FileMetricAdapter{root: dir}
}

func (a *FileMetricAdapter) Name() string { return "file" }

type ndjsonExemplar struct {
	TS        time.Time         `json:"ts"`
	TraceID   string            `json:"trace_id"`
	SpanID    string            `json:"span_id"`
	Service   string            `json:"service"`
	Operation string            `json:"operation"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels"`
}

func (a *FileMetricAdapter) readTenantExemplars(tid model.TenantID) ([]ndjsonExemplar, error) {
	dir := filepath.Join(a.root, string(tid))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ndjsonExemplar
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var ex ndjsonExemplar
			if err := json.Unmarshal([]byte(line), &ex); err != nil {
				continue
			}
			out = append(out, ex)
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ExemplarsFor implements MetricAdapter (FR-F07-2): exemplar-linked metric
// samples for (service, operation) within w.
func (a *FileMetricAdapter) ExemplarsFor(ctx context.Context, tid model.TenantID, service, operation string, w model.Window) ([]model.Exemplar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	exs, err := a.readTenantExemplars(tid)
	if err != nil {
		return nil, err
	}
	var out []model.Exemplar
	for _, e := range exs {
		if e.Service != service || e.Operation != operation || !inWindow(e.TS, w) {
			continue
		}
		tidB, _ := decodeTraceID(e.TraceID)
		sidB, _ := decodeSpanID(e.SpanID)
		out = append(out, model.Exemplar{
			TraceID:   tidB,
			SpanID:    sidB,
			Timestamp: e.TS,
			Value:     e.Value,
			Labels:    e.Labels,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

// Range implements MetricAdapter.Range by bucketing matching exemplar
// samples into stepSeconds-wide buckets and averaging. A minimal stand-in
// for true PromQL range evaluation — sufficient for the dev/offline path.
func (a *FileMetricAdapter) Range(ctx context.Context, tid model.TenantID, templateID string, params map[string]string, w model.Window, stepSeconds int) (model.Series, error) {
	if err := ctx.Err(); err != nil {
		return model.Series{}, err
	}
	service := params["service"]
	operation := params["operation"]
	exs, err := a.readTenantExemplars(tid)
	if err != nil {
		return model.Series{}, err
	}
	if stepSeconds <= 0 {
		stepSeconds = 60
	}
	step := time.Duration(stepSeconds) * time.Second
	type bucket struct {
		sum   float64
		count int
		ts    time.Time
	}
	buckets := map[int64]*bucket{}
	for _, e := range exs {
		if (service != "" && e.Service != service) || (operation != "" && e.Operation != operation) {
			continue
		}
		if !inWindow(e.TS, w) {
			continue
		}
		key := e.TS.Unix() / int64(stepSeconds)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{ts: time.Unix(key*int64(stepSeconds), 0).UTC()}
			buckets[key] = b
		}
		b.sum += e.Value
		b.count++
	}
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	points := make([]model.SeriesPoint, 0, len(keys))
	for _, k := range keys {
		b := buckets[k]
		points = append(points, model.SeriesPoint{Timestamp: b.ts, Value: b.sum / float64(b.count)})
	}
	_ = step
	return model.Series{
		Labels: map[string]string{"template": templateID, "service": service, "operation": operation},
		Points: points,
	}, nil
}

func (a *FileMetricAdapter) Capabilities() MetricCapabilities {
	return MetricCapabilities{TenantScoped: true, MaxLookback: 0}
}

func (a *FileMetricAdapter) Health(ctx context.Context) model.HealthReport {
	return model.HealthReport{Healthy: true, Message: "file adapter", CheckedAt: time.Now().UTC()}
}

func (a *FileMetricAdapter) Close() error { return nil }

// WriteDevLogLine is a small test/dev-fixture helper: appends one NDJSON log
// line for tenant tid under dir/<tid>/<file>.ndjson, creating directories as
// needed.
func WriteDevLogLine(dir string, tid model.TenantID, file string, ts time.Time, traceID model.TraceID, spanID model.SpanID, service, level, body string, attrs map[string]string) error {
	tdir := filepath.Join(dir, string(tid))
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		return err
	}
	l := ndjsonLogLine{
		TS:      ts,
		TraceID: hex.EncodeToString(traceID[:]),
		SpanID:  hex.EncodeToString(spanID[:]),
		Service: service,
		Level:   level,
		Body:    body,
		Attrs:   attrs,
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(tdir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, string(b))
	return err
}

// WriteDevExemplar is a small test/dev-fixture helper mirroring
// WriteDevLogLine for the metric adapter's exemplar files.
func WriteDevExemplar(dir string, tid model.TenantID, file string, ts time.Time, traceID model.TraceID, spanID model.SpanID, service, operation string, value float64, labels map[string]string) error {
	tdir := filepath.Join(dir, string(tid))
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		return err
	}
	e := ndjsonExemplar{
		TS:        ts,
		TraceID:   hex.EncodeToString(traceID[:]),
		SpanID:    hex.EncodeToString(spanID[:]),
		Service:   service,
		Operation: operation,
		Value:     value,
		Labels:    labels,
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(tdir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, string(b))
	return err
}
