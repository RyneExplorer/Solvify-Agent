package observability

import (
	"expvar"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
)

type MetricSample struct {
	Name   string            `json:"name"`
	Type   MetricType        `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
}

type HistogramBuckets struct {
	UpperBounds []float64
}

var DefaultHistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

const defaultExpvarMapName = "solvify_observability"

var (
	globalStore     *MetricStore
	globalStoreOnce sync.Once
)

func GlobalMetricStore(maxCardinality int) *MetricStore {
	globalStoreOnce.Do(func() {
		globalStore = NewMetricStore(maxCardinality)
	})
	return globalStore
}

type labelsCardinalityGuard struct {
	mu       sync.Mutex
	registry map[string]map[string]struct{}
	limit    int
	dropped  atomic.Int64
}

func newCardinalityGuard(limit int) *labelsCardinalityGuard {
	if limit <= 0 {
		limit = 500
	}
	return &labelsCardinalityGuard{registry: map[string]map[string]struct{}{}, limit: limit}
}

func (g *labelsCardinalityGuard) Allow(name, signature string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	reg, ok := g.registry[name]
	if !ok {
		reg = map[string]struct{}{}
		g.registry[name] = reg
	}
	if _, ok := reg[signature]; ok {
		return true
	}
	if len(reg) >= g.limit {
		g.dropped.Add(1)
		return false
	}
	reg[signature] = struct{}{}
	return true
}

func (g *labelsCardinalityGuard) Dropped() int64 {
	return g.dropped.Load()
}

type MetricStore struct {
	mu       sync.RWMutex
	counters map[string]map[string]*atomic.Int64
	gauges   map[string]map[string]*atomic.Int64
	histos   map[string]map[string]*histogram
	card     *labelsCardinalityGuard
	root     *expvar.Map
}

type histogram struct {
	mu      sync.Mutex
	buckets []int64
	sum     float64
	count   int64
	bounds  []float64
}

func newHistogram(bounds []float64) *histogram {
	if len(bounds) == 0 {
		bounds = DefaultHistogramBuckets
	}
	cp := make([]float64, len(bounds))
	copy(cp, bounds)
	sort.Float64s(cp)
	return &histogram{bounds: cp, buckets: make([]int64, len(cp)+1)}
}

func (h *histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += v
	idx := sort.SearchFloat64s(h.bounds, v)
	if idx > len(h.buckets)-1 {
		idx = len(h.buckets) - 1
	}
	h.buckets[idx]++
}

func (h *histogram) snapshot() (count int64, sum float64, buckets []int64, bounds []float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	count = h.count
	sum = h.sum
	buckets = append(buckets, h.buckets...)
	bounds = append(bounds, h.bounds...)
	return
}

func NewMetricStore(maxCardinality int) *MetricStore {
	var root *expvar.Map
	if existing := expvar.Get(defaultExpvarMapName); existing != nil {
		if m, ok := existing.(*expvar.Map); ok {
			root = m
		}
	}
	if root == nil {
		root = expvar.NewMap(defaultExpvarMapName)
	}
	s := &MetricStore{
		counters: map[string]map[string]*atomic.Int64{},
		gauges:   map[string]map[string]*atomic.Int64{},
		histos:   map[string]map[string]*histogram{},
		card:     newCardinalityGuard(maxCardinality),
		root:     root,
	}
	if expvar.Get(defaultExpvarMapName+".start_time_seconds") == nil {
		s.root.Set("start_time_seconds", expvar.Func(func() any { return float64(time.Now().Unix()) }))
	}
	if expvar.Get(defaultExpvarMapName+".label_dropped_total") == nil {
		s.root.Set("label_dropped_total", expvar.Func(func() any { return s.card.Dropped() }))
	}
	return s
}

func signature(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('|')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
	}
	return sb.String()
}

func (s *MetricStore) Incr(name string, labels map[string]string, delta int64) {
	if name == "" || delta == 0 {
		return
	}
	sig := signature(labels)
	if !s.card.Allow(name, sig) {
		return
	}
	s.mu.RLock()
	row, ok := s.counters[name]
	s.mu.RUnlock()
	if !ok {
		s.mu.Lock()
		if row, ok = s.counters[name]; !ok {
			row = map[string]*atomic.Int64{}
			s.counters[name] = row
		}
		s.mu.Unlock()
	}
	c, ok := row[sig]
	if !ok {
		newC := new(atomic.Int64)
		s.mu.Lock()
		if c, ok = row[sig]; !ok {
			row[sig] = newC
			c = newC
		}
		s.mu.Unlock()
	}
	c.Add(delta)
}

func (s *MetricStore) SetGauge(name string, labels map[string]string, value int64) {
	if name == "" {
		return
	}
	sig := signature(labels)
	if !s.card.Allow(name, sig) {
		return
	}
	s.mu.RLock()
	row, ok := s.gauges[name]
	s.mu.RUnlock()
	if !ok {
		s.mu.Lock()
		if row, ok = s.gauges[name]; !ok {
			row = map[string]*atomic.Int64{}
			s.gauges[name] = row
		}
		s.mu.Unlock()
	}
	g, ok := row[sig]
	if !ok {
		newG := new(atomic.Int64)
		s.mu.Lock()
		if g, ok = row[sig]; !ok {
			row[sig] = newG
			g = newG
		}
		s.mu.Unlock()
	}
	g.Store(value)
}

func (s *MetricStore) Observe(name string, labels map[string]string, value float64, bounds []float64) {
	if name == "" {
		return
	}
	sig := signature(labels)
	if !s.card.Allow(name, sig) {
		return
	}
	s.mu.RLock()
	row, ok := s.histos[name]
	s.mu.RUnlock()
	if !ok {
		s.mu.Lock()
		if row, ok = s.histos[name]; !ok {
			row = map[string]*histogram{}
			s.histos[name] = row
		}
		s.mu.Unlock()
	}
	h, ok := row[sig]
	if !ok {
		newH := newHistogram(bounds)
		s.mu.Lock()
		if h, ok = row[sig]; !ok {
			row[sig] = newH
			h = newH
		}
		s.mu.Unlock()
	}
	h.Observe(value)
}

func (s *MetricStore) SnapshotJSON() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]any{
		"label_cardinality_dropped_total": s.card.Dropped(),
		"generated_at_seconds":            time.Now().Unix(),
	}
	processLabels := func(labels map[string]string) []any {
		arr := make([]any, 0, len(labels))
		for k, v := range labels {
			arr = append(arr, map[string]any{"name": k, "value": v})
		}
		return arr
	}
	parseLabels := func(sig string) map[string]string {
		if sig == "" {
			return nil
		}
		out := map[string]string{}
		for _, p := range strings.Split(sig, "|") {
			kv := strings.SplitN(p, "=", 2)
			if len(kv) == 2 {
				out[kv[0]] = kv[1]
			}
		}
		return out
	}
	counters := []any{}
	names := make([]string, 0, len(s.counters))
	for n := range s.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sigs := make([]string, 0, len(s.counters[n]))
		for ss := range s.counters[n] {
			sigs = append(sigs, ss)
		}
		sort.Strings(sigs)
		for _, ss := range sigs {
			counters = append(counters, map[string]any{
				"name":   n,
				"type":   "counter",
				"labels": processLabels(parseLabels(ss)),
				"value":  float64(s.counters[n][ss].Load()),
			})
		}
	}
	out["counters"] = counters
	gauges := []any{}
	names = names[:0]
	for n := range s.gauges {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sigs := make([]string, 0, len(s.gauges[n]))
		for ss := range s.gauges[n] {
			sigs = append(sigs, ss)
		}
		sort.Strings(sigs)
		for _, ss := range sigs {
			gauges = append(gauges, map[string]any{
				"name":   n,
				"type":   "gauge",
				"labels": processLabels(parseLabels(ss)),
				"value":  float64(s.gauges[n][ss].Load()),
			})
		}
	}
	out["gauges"] = gauges
	histos := []any{}
	names = names[:0]
	for n := range s.histos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sigs := make([]string, 0, len(s.histos[n]))
		for ss := range s.histos[n] {
			sigs = append(sigs, ss)
		}
		sort.Strings(sigs)
		for _, ss := range sigs {
			count, sum, buckets, bounds := s.histos[n][ss].snapshot()
			bucketMap := make([]any, 0, len(bounds)+1)
			var cum int64
			for i := 0; i < len(bounds); i++ {
				cum += buckets[i]
				bucketMap = append(bucketMap, map[string]any{
					"le":          bounds[i],
					"cumulative":  cum,
					"delta_count": buckets[i],
				})
			}
			if len(buckets) > len(bounds) {
				cum += buckets[len(bounds)]
				bucketMap = append(bucketMap, map[string]any{
					"le":          "+Inf",
					"cumulative":  cum,
					"delta_count": buckets[len(bounds)],
				})
			}
			histos = append(histos, map[string]any{
				"name":     n,
				"type":     "histogram",
				"labels":   processLabels(parseLabels(ss)),
				"count":    count,
				"sum":      sum,
				"buckets":  bucketMap,
			})
		}
	}
	out["histograms"] = histos
	return out
}

func (s *MetricStore) PrometheusText() (string, error) {
	snap := s.SnapshotJSON()
	var sb strings.Builder
	appendLabels := func(labels []any) {
		first := true
		for _, raw := range labels {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			k := fmt.Sprintf("%s", m["name"])
			v := fmt.Sprintf("%s", m["value"])
			if !first {
				sb.WriteByte(',')
			}
			first = false
			sb.WriteString(k)
			sb.WriteString(`="`)
			sb.WriteString(strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`))
			sb.WriteByte('"')
		}
	}
	counters, _ := snap["counters"].([]any)
	for _, raw := range counters {
		m := raw.(map[string]any)
		name := m["name"].(string)
		labels, _ := m["labels"].([]any)
		value := m["value"].(float64)
		sb.WriteString("# HELP ")
		sb.WriteString(name)
		sb.WriteString(" solvify counter metric\n# TYPE ")
		sb.WriteString(name)
		sb.WriteString(" counter\n")
		sb.WriteString(name)
		sb.WriteByte('{')
		appendLabels(labels)
		sb.WriteString("} ")
		fmt.Fprintf(&sb, "%.0f\n", value)
	}
	gauges, _ := snap["gauges"].([]any)
	for _, raw := range gauges {
		m := raw.(map[string]any)
		name := m["name"].(string)
		labels, _ := m["labels"].([]any)
		value := m["value"].(float64)
		sb.WriteString("# HELP ")
		sb.WriteString(name)
		sb.WriteString(" solvify gauge metric\n# TYPE ")
		sb.WriteString(name)
		sb.WriteString(" gauge\n")
		sb.WriteString(name)
		sb.WriteByte('{')
		appendLabels(labels)
		sb.WriteString("} ")
		fmt.Fprintf(&sb, "%.0f\n", value)
	}
	histos, _ := snap["histograms"].([]any)
	for _, raw := range histos {
		m := raw.(map[string]any)
		name := m["name"].(string)
		labels, _ := m["labels"].([]any)
		count := m["count"].(int64)
		sum := m["sum"].(float64)
		buckets, _ := m["buckets"].([]any)
		sb.WriteString("# HELP ")
		sb.WriteString(name)
		sb.WriteString(" solvify histogram metric\n# TYPE ")
		sb.WriteString(name)
		sb.WriteString(" histogram\n")
		for _, b := range buckets {
			bm := b.(map[string]any)
			le := fmt.Sprintf("%s", bm["le"])
			cum := bm["cumulative"].(int64)
			sb.WriteString(name)
			sb.WriteString("_bucket{le=\"")
			sb.WriteString(le)
			sb.WriteByte('"')
			if len(labels) > 0 {
				sb.WriteByte(',')
				appendLabels(labels)
			}
			sb.WriteString("} ")
			fmt.Fprintf(&sb, "%d\n", cum)
		}
		sb.WriteString(name)
		sb.WriteString("_sum{")
		appendLabels(labels)
		sb.WriteString("} ")
		fmt.Fprintf(&sb, "%f\n", sum)
		sb.WriteString(name)
		sb.WriteString("_count{")
		appendLabels(labels)
		sb.WriteString("} ")
		fmt.Fprintf(&sb, "%d\n", count)
	}
	return sb.String(), nil
}
