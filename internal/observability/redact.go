package observability

// 本文件是可观测性数据的「脱敏出口」。
//
// 背景（一次真实的漏网）：落库路径与日志路径是两条独立代码路径 ——
//   - 日志路径：Recorder → BatchSink → LogSink.Write，LogSink 会先 clean 自己的副本；
//   - 落库路径：Recorder 直接调 dbSink.WriteTraces / WriteFeedbacks / WriteAgentSteps，
//     **完全不经过 BatchSink 扇出**（app.go 用的是 NewRecorderWithDBSink，
//     dbSink 是单独赋值给 r.dbSink 的字段，不是 extraSinks）。
//
// 后果是：只要某个出口忘了清洁，用户原文就会直接进表。事实上 RecordFeedback 就是这样
// —— 日志里看到的是掩码后的评论，chat_feedbacks.comment 里却是原文。
//
// 所以这里提供一组**纯函数式、不改入参**的脱敏助手，约定为所有落库 / 日志出口的统一入口：
//   - sanitizeRecord   ：SinkRecord 级（LogSink 用）
//   - sanitizeTrace    ：深拷贝整棵 span 树后脱敏（DB trace 出口用）
//   - sanitizeFeedback ：反馈（DB feedback 出口用）
//   - sanitizeAgentStep：Agent 步骤（DB agent_step 出口用）
//
// 「不改入参」是硬约束，不是风格偏好：
//  1. 落库出口（observabilityRepository.WriteTraces）会就地对树做 stripInternalSpanAttrs
//     改写 + json.Marshal。若与 LogSink 共享同一棵活树，而 LogSink 跑在 BatchSink 的后台
//     goroutine 上，就是对同一结构体的并发读写。
//  2. cleanSpan 曾经用 `s.Children[i] = &cp` 就地把子节点换成脱敏副本 —— 调用方以为
//     「只是拿了一份干净副本去打印」，实际上手里的树已经被改了一半。

// sanitizeRecord 返回 rec 的脱敏副本，不修改入参。
//
// pii 为 nil 时退化为「只做浅拷贝」（与历史行为一致：不脱敏，但也不共享顶层结构体）。
func sanitizeRecord(rec *SinkRecord, pii *PIISanitizer) *SinkRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	if pii == nil {
		return &cp
	}
	cp.Attrs = pii.SanitizeAttrs(rec.Attrs)
	cp.Trace = sanitizeTrace(rec.Trace, pii)
	cp.Feedback = sanitizeFeedback(rec.Feedback, pii)
	cp.AgentStep = sanitizeAgentStep(rec.AgentStep, pii)
	return &cp
}

// sanitizeTrace 返回 t 的脱敏副本，不修改入参（含 Root 指向的整棵 span 树）。
//
// 无论 pii 是否为 nil，Root 都会被深拷贝：落库出口会就地把树改写成「可落库形态」，
// 与日志出口共享同一棵树是并发隐患（见文件头注释）。
func sanitizeTrace(t *Trace, pii *PIISanitizer) *Trace {
	if t == nil {
		return nil
	}
	cp := &Trace{
		ID:           t.ID,
		RequestID:    t.RequestID,
		UserID:       t.UserID,
		SessionID:    t.SessionID,
		SampleRate:   t.SampleRate,
		Sampled:      t.Sampled,
		OTelTraceID:  t.OTelTraceID,
		OTelExported: t.OTelExported,
	}
	if t.Root == nil {
		return cp
	}
	root := cloneSpan(t.Root)
	if root == nil {
		return cp
	}
	if pii != nil {
		cleaned := cleanSpan(*root, pii)
		root = &cleaned
	}
	cp.Root = root
	return cp
}

// sanitizeFeedback 返回 f 的脱敏副本，不修改入参。
func sanitizeFeedback(f *Feedback, pii *PIISanitizer) *Feedback {
	if f == nil {
		return nil
	}
	cp := *f
	if len(f.Reasons) > 0 {
		cp.Reasons = append([]string(nil), f.Reasons...)
	}
	if pii == nil {
		return &cp
	}
	cp.Comment = pii.SanitizeString(cp.Comment)
	for i, r := range cp.Reasons {
		cp.Reasons[i] = pii.SanitizeString(r)
	}
	return &cp
}

// sanitizeAgentStep 返回 step 的脱敏副本，不修改入参。
func sanitizeAgentStep(st *AgentStep, pii *PIISanitizer) *AgentStep {
	if st == nil {
		return nil
	}
	cp := *st
	if pii == nil {
		return &cp
	}
	cp.ThinkingSummary = pii.SanitizeString(cp.ThinkingSummary)
	cp.ToolInputMasked = pii.SanitizeString(cp.ToolInputMasked)
	cp.ToolResultSummary = pii.SanitizeString(cp.ToolResultSummary)
	cp.ToolError = pii.SanitizeString(cp.ToolError)
	return &cp
}

// cleanSpan 返回脱敏后的 Span **副本**。
//
// 硬约束：不修改入参 s 的任何内存 ——
//   - Attrs / Error 直接赋在返回值上（值拷贝，天然隔离）；
//   - Events / Children 一律重建新切片再填脱敏后的元素，
//     绝不写回入参的底层数组（旧实现 `s.Children[i] = &cp` 正是踩了这个坑）。
//
// 旧实现还有一个越界隐患：`cp := *s.Children[i]` 对 nil 子节点直接解引用 panic。
// 这里统一跳过 nil，与 cloneSpan 的容错保持一致。
func cleanSpan(s Span, pii *PIISanitizer) Span {
	if pii == nil {
		return s
	}
	if len(s.Attrs) > 0 {
		s.Attrs = pii.SanitizeAttrs(s.Attrs)
	}
	if s.Error != "" {
		s.Error = pii.SanitizeString(s.Error)
	}
	if len(s.Events) > 0 {
		events := make([]*SpanEvent, 0, len(s.Events))
		for _, e := range s.Events {
			if e == nil {
				continue
			}
			ce := *e
			if len(ce.Attrs) > 0 {
				ce.Attrs = pii.SanitizeAttrs(ce.Attrs)
			}
			events = append(events, &ce)
		}
		s.Events = events
	}
	if len(s.Children) > 0 {
		children := make([]*Span, 0, len(s.Children))
		for _, c := range s.Children {
			if c == nil {
				continue
			}
			cc := cleanSpan(*c, pii)
			children = append(children, &cc)
		}
		s.Children = children
	}
	return s
}
