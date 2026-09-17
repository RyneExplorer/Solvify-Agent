package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/observability"
)

// ─── 空回答守卫 rejectEmptyAnswer 的单元测试 ───────────────────────────────
//
// 快速模式与深度模式共用这个守卫，两种模式此前的错误行为都已写进函数注释：
// 快速模式照发 done 并落库空消息（空白气泡 + 污染历史），深度模式裸 return
// （前端永远等不到终态事件）。这里把守卫的判定与产出用单测钉住，
// 两种模式各自的端到端行为另有 chat_service_graph_quick_test.go 覆盖。

// recordingRecorder 只记录 Incr / MarkTraceError 两类调用。
// 嵌入接口以保留其余方法（本用例不会触达），避免为一次单测实现 16 个空方法。
type recordingRecorder struct {
	observability.Recorder

	incrCalls    []string
	incrLabels   []map[string]string
	markedErrors []error
}

func (r *recordingRecorder) Incr(_ context.Context, metric string, labels map[string]string, _ int64) {
	r.incrCalls = append(r.incrCalls, metric)
	r.incrLabels = append(r.incrLabels, labels)
}

func (r *recordingRecorder) MarkTraceError(_ context.Context, err error) {
	r.markedErrors = append(r.markedErrors, err)
}

// 有正文时直接放行，且不产生任何事件或指标。
func TestRejectEmptyAnswer_NonEmptyPassesThrough(t *testing.T) {
	eventCh := make(chan dto.StreamEvent, 4)
	rec := &recordingRecorder{}

	if rejectEmptyAnswer(context.Background(), eventCh, rec, "quick", "s1", "m1", "msg1", "这是一段正常回答", "") {
		t.Fatal("非空回答不应被拦截")
	}
	if len(eventCh) != 0 {
		t.Errorf("非空回答不应发出任何事件，实际 %d 条", len(eventCh))
	}
	if len(rec.incrCalls) != 0 || len(rec.markedErrors) != 0 {
		t.Errorf("非空回答不应打点: incr=%v, markedErrors=%v", rec.incrCalls, rec.markedErrors)
	}
}

// 空串与纯空白都必须被拦截，并发出可重试的 error 终态事件（obs 传 nil，同时验证空安全）。
func TestRejectEmptyAnswer_EmptyOrBlankIsRejected(t *testing.T) {
	for _, content := range []string{"", " ", "\n", " \t\r\n "} {
		eventCh := make(chan dto.StreamEvent, 4)

		if !rejectEmptyAnswer(context.Background(), eventCh, nil, "deep", "s1", "m1", "msg1", content, "sources=0, steps=0") {
			t.Fatalf("content=%q 应被判定为空回答", content)
		}
		if len(eventCh) != 1 {
			t.Fatalf("content=%q 应恰好发出 1 条终态事件，实际 %d 条", content, len(eventCh))
		}

		ev := <-eventCh
		if ev.Type != "error" {
			t.Errorf("content=%q 事件类型应为 error，实际 %q", content, ev.Type)
		}
		if !ev.Done || !ev.Retryable {
			t.Errorf("content=%q 应为终态且可重试: Done=%v, Retryable=%v", content, ev.Done, ev.Retryable)
		}
		if !strings.Contains(ev.Title, "未返回内容") {
			t.Errorf("content=%q 标题=%q，期望包含「未返回内容」", content, ev.Title)
		}
	}
}

// 打点带 mode 标签，便于区分是哪种模式在返回空回答；同时标记哨兵错误。
func TestRejectEmptyAnswer_ReportsModeLabelAndSentinel(t *testing.T) {
	for _, mode := range []string{"quick", "deep"} {
		eventCh := make(chan dto.StreamEvent, 4)
		rec := &recordingRecorder{}

		if !rejectEmptyAnswer(context.Background(), eventCh, rec, mode, "s1", "m1", "msg1", "", "x=1") {
			t.Fatalf("mode=%s 空回答应被拦截", mode)
		}

		if len(rec.incrCalls) != 1 || rec.incrCalls[0] != "chat_empty_answer_total" {
			t.Errorf("mode=%s 指标名不对: %v", mode, rec.incrCalls)
		}
		if len(rec.incrLabels) != 1 || rec.incrLabels[0]["mode"] != mode {
			t.Errorf("mode=%s 指标标签不对: %v", mode, rec.incrLabels)
		}
		if len(rec.markedErrors) != 1 || !errors.Is(rec.markedErrors[0], errEmptyAnswer) {
			t.Errorf("mode=%s 未标记哨兵错误 errEmptyAnswer: %v", mode, rec.markedErrors)
		}
	}
}

// ─── 事件下发的 ctx 守卫（审查报告 P0-1 回归） ─────────────────────────────
//
// 故障注入口径：eventCh 无缓冲且无人消费，等价于「客户端断连后 gin 的 c.Stream
// 不再 drain eventCh」。此时 ctx 已取消，事件必须被丢弃并立即返回；否则发送方
// 永久阻塞，每次断连都会泄漏一份 goroutine + DB 连接 + 上游 LLM 流。

// ctx 已取消 + 无消费者：必须立即返回，而不是永久阻塞。
func TestSendErrorEvent_ReturnsImmediatelyWhenCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	eventCh := make(chan dto.StreamEvent) // 故意不缓冲、不消费

	done := make(chan struct{})
	go func() {
		defer close(done)
		sendErrorEvent(ctx, eventCh, errors.New("boom"), "")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 已取消但 sendErrorEvent 仍阻塞 —— 断连会拖死整条链路（P0-1 回归）")
	}
}

// 反向保护：ctx 存活且消费者就绪时事件必须真的送达。
// 缺了这条，把 Send 写成「无条件丢弃」也能让上面的用例通过。
func TestSendErrorEvent_DeliversWhenCtxAlive(t *testing.T) {
	eventCh := make(chan dto.StreamEvent, 1)
	sendErrorEvent(context.Background(), eventCh, errors.New("boom"), "原始描述")

	select {
	case ev := <-eventCh:
		if ev.Type != "error" || !ev.Done {
			t.Errorf("事件类型/终态不对: %+v", ev)
		}
	default:
		t.Fatal("ctx 存活时事件被丢弃 —— 正常路径不应降级")
	}
}
