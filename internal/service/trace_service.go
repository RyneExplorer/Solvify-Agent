package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"

	"gorm.io/datatypes"
)

func (s *chatService) buildTraceResponse(t *entity.ChatTrace, includeAgentDetail bool) TraceResponse {
	if t == nil {
		return TraceResponse{}
	}
	resp := TraceResponse{
		ID:         t.ID,
		RequestID:  t.RequestID,
		UserID:     t.UserID,
		SessionID:  t.SessionID,
		SearchMode: extractSearchMode(t.Attrs),
		SampleRate: t.SampleRate,
		Sampled:    t.Sampled,
		DurationMs: t.DurationMs,
		Status:     t.Status,
		Error:      t.Error,
		Attrs:      t.Attrs,
		SpanTree:   t.SpanTree,
		CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if !includeAgentDetail || s.obsRepo == nil {
		return resp
	}
	task, steps, _ := s.obsRepo.FindByTraceID(context.Background(), t.ID)
	resp.AgentTask = chatAgentTaskEntityToResponse(task)
	resp.AgentSteps = chatAgentStepEntityToResponse(steps)
	// 有 AgentStep 信息时，把 TotalSteps / ToolCalls 反填回 AgentTask（如果之前 MarkEnded 没填充好）
	if resp.AgentTask != nil && len(resp.AgentSteps) > 0 {
		toolCalls := 0
		for _, st := range resp.AgentSteps {
			if st.ToolName != "" && st.ToolName != "llm.reasoning" {
				toolCalls++
			}
		}
		if resp.AgentTask.TotalSteps <= 0 {
			resp.AgentTask.TotalSteps = len(resp.AgentSteps)
		}
		if resp.AgentTask.ToolCalls <= 0 {
			resp.AgentTask.ToolCalls = toolCalls
		}
	}
	return resp
}

// GetTrace 根据追踪 ID 查询追踪详情
func (s *chatService) GetTrace(ctx context.Context, userID, traceID string, isAdmin bool) (*TraceResponse, error) {
	if s.obsRepo == nil || traceID == "" {
		return nil, fmt.Errorf("trace 存储未启用")
	}
	t, err := s.obsRepo.FindByID(ctx, traceID)
	if err != nil {
		return nil, fmt.Errorf("trace 不存在: %w", err)
	}
	if !isAdmin && t.UserID != userID {
		return nil, fmt.Errorf("无权限访问该 trace")
	}
	resp := s.buildTraceResponse(t, true)
	return &resp, nil
}

// ListSessionTraces 分页查询会话维度的追踪列表
func (s *chatService) ListSessionTraces(ctx context.Context, userID, sessionID string, isAdmin bool, offset, limit int) (TraceListResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if s.obsRepo == nil {
		return TraceListResponse{Total: 0, Traces: []any{}}, nil
	}
	if !isAdmin {
		if err := s.validateSession(ctx, userID, sessionID); err != nil {
			return TraceListResponse{}, err
		}
	}
	list, total, err := s.obsRepo.ListBySession(ctx, sessionID, userID, offset, limit)
	if isAdmin {
		list, total, err = s.obsRepo.ListAll(ctx, sessionID, "", offset, limit)
	}
	if err != nil {
		return TraceListResponse{}, err
	}
	items := make([]any, 0, len(list))
	for i := range list {
		items = append(items, s.buildTraceResponse(&list[i], false))
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

// AdminListTraces 管理员分页查询全量追踪列表
func (s *chatService) AdminListTraces(ctx context.Context, sessionID string, rating int, status string, offset, limit int) (TraceListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	if s.obsRepo == nil {
		return TraceListResponse{Total: 0, Traces: []any{}}, nil
	}
	list, total, err := s.obsRepo.ListAll(ctx, sessionID, status, offset, limit)
	if err != nil {
		return TraceListResponse{}, err
	}
	items := make([]any, 0, len(list))
	for i := range list {
		items = append(items, s.buildTraceResponse(&list[i], false))
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

// GetMetricsSnapshot 获取可观测性指标快照
func (s *chatService) GetMetricsSnapshot() (map[string]any, error) {
	if s.obs == nil {
		return nil, fmt.Errorf("observability 未启用")
	}
	raw, err := s.obs.MetricsSnapshot()
	if err != nil {
		return nil, err
	}
	rawCounters, _ := raw["counters"].([]any)
	rawGauges, _ := raw["gauges"].([]any)
	rawHistos, _ := raw["histograms"].([]any)
	labelDropped, _ := raw["label_cardinality_dropped_total"].(int64)
	var generatedTs string
	if ts, ok := raw["generated_at_seconds"].(int64); ok {
		generatedTs = time.Unix(ts, 0).Format(time.RFC3339)
	}

	samplingRate := 0.0
	labelCardLimit := 0
	bufferDropped := int64(0)
	piiMasked := int64(0)
	if ss, ok := raw["sink_stats"].(map[string]any); ok {
		if v, ok := ss["dropped_records_total"].(int64); ok {
			bufferDropped = v
		}
	}
	if c, ok := s.obs.(interface{ SamplingRate() float64 }); ok {
		samplingRate = c.SamplingRate()
	} else if cfg := s.cfgObservability(); cfg != nil {
		samplingRate = cfg.SamplingRate
	}

	labelsToMap := func(raw []any) map[string]string {
		out := map[string]string{}
		for _, r := range raw {
			if m, ok := r.(map[string]any); ok {
				k, _ := m["name"].(string)
				v, _ := m["value"].(string)
				if k != "" {
					out[k] = v
				}
			}
		}
		return out
	}
	type namedSamples struct {
		Name    string
		Help    string
		Samples []any
	}
	groupByMetric := func(rows []any) []namedSamples {
		groups := map[string]*namedSamples{}
		order := []string{}
		for _, r := range rows {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			if _, seen := groups[name]; !seen {
				groups[name] = &namedSamples{Name: name}
				order = append(order, name)
			}
			sample := map[string]any{}
			labelsRaw, _ := m["labels"].([]any)
			labelsM := labelsToMap(labelsRaw)
			if len(labelsM) > 0 {
				sample["labels"] = labelsM
			}
			switch {
			case m["value"] != nil:
				if v, ok := m["value"].(float64); ok {
					sample["value"] = int64(v)
				} else {
					sample["value"] = m["value"]
				}
			case m["count"] != nil:
				if v, ok := m["count"].(int64); ok {
					sample["count"] = v
				} else {
					sample["count"] = m["count"]
				}
				if sum, ok := m["sum"].(float64); ok {
					sample["sum"] = sum
				}
				if buckets, ok := m["buckets"].([]any); ok {
					outB := make([]any, 0, len(buckets))
					for _, b := range buckets {
						bm, ok := b.(map[string]any)
						if !ok {
							continue
						}
						le := bm["le"]
						// JS Number.MAX_SAFE_INTEGER = 9007199254740991，+Inf 语义替换为该值，保证前端 TS number 类型一致
						if s, _ := le.(string); s == "+Inf" {
							le = float64(9007199254740991)
						} else if le == "+inf" || le == "Inf" {
							le = float64(9007199254740991)
						}
						cnt, _ := bm["delta_count"].(int64)
						outB = append(outB, map[string]any{"le": le, "count": cnt})
					}
					sample["buckets"] = outB
				}
			}
			groups[name].Samples = append(groups[name].Samples, sample)
		}
		out := make([]namedSamples, 0, len(order))
		for _, n := range order {
			out = append(out, *groups[n])
		}
		return out
	}
	cGroups := groupByMetric(rawCounters)
	gGroups := groupByMetric(rawGauges)
	hGroups := groupByMetric(rawHistos)
	counters := make([]any, 0, len(cGroups))
	for _, c := range cGroups {
		counters = append(counters, map[string]any{"name": c.Name, "help": "", "samples": c.Samples})
	}
	gauges := make([]any, 0, len(gGroups))
	for _, g := range gGroups {
		gauges = append(gauges, map[string]any{"name": g.Name, "help": "", "samples": g.Samples})
	}
	histos := make([]any, 0, len(hGroups))
	for _, h := range hGroups {
		histos = append(histos, map[string]any{"name": h.Name, "help": "", "samples": h.Samples})
	}
	return map[string]any{
		"ts":                              generatedTs,
		"counters":                        counters,
		"gauges":                          gauges,
		"histograms":                      histos,
		"sampling_rate":                   samplingRate,
		"label_cardinality_limit":         labelCardLimit,
		"buffer_dropped_total":            bufferDropped,
		"pii_masked_total":                piiMasked,
		"label_cardinality_dropped_total": labelDropped,
	}, nil
}

func (s *chatService) cfgObservability() *config.ObservabilityConfig {
	if s.obs == nil {
		return nil
	}
	type cfgProvider interface {
		Config() config.ObservabilityConfig
	}
	if p, ok := s.obs.(cfgProvider); ok {
		c := p.Config()
		return &c
	}
	return nil
}

func ratingLabel(r int) string {
	switch r {
	case 1:
		return "up"
	case -1:
		return "down"
	default:
		return "unknown"
	}
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func reasonTagOrDefault(tag string) string {
	if tag == "" {
		return "none"
	}
	return tag
}

func metadataAsMap(raw datatypes.JSON) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// extractSearchMode 从 Attrs JSON / SpanTree JSON 里取 search_mode 作为 TraceResponse 顶层字段
// 兼容 datatypes.JSON（GORM）、map[string]any（内存对象）、[]byte 三种来源；
// 找不到时再回退硬解析 ChatTrace.SpanTree root 的 attrs.search_mode，避免新老数据过渡时为空
func extractSearchMode(attrs any, spanTreeHint ...datatypes.JSON) string {
	if s := extractSearchModeFromAny(attrs); s != "" {
		return s
	}
	for _, st := range spanTreeHint {
		if len(st) == 0 {
			continue
		}
		var root struct {
			Attrs map[string]any `json:"attrs"`
		}
		if err := json.Unmarshal(st, &root); err == nil {
			if s, ok := root.Attrs["search_mode"].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func extractSearchModeFromAny(attrs any) string {
	if attrs == nil {
		return ""
	}
	switch v := attrs.(type) {
	case map[string]any:
		if s, ok := v["search_mode"].(string); ok {
			return s
		}
	case datatypes.JSON:
		if len(v) == 0 {
			return ""
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err == nil {
			if s, ok := m["search_mode"].(string); ok {
				return s
			}
		}
	case []byte:
		if len(v) == 0 {
			return ""
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err == nil {
			if s, ok := m["search_mode"].(string); ok {
				return s
			}
		}
	}
	return ""
}
