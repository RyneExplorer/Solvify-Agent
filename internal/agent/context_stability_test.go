package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

func collectContextStressEvents(ctx context.Context, engine *Engine, req Request, cm *deepTestModel) ([]Event, error) {
	events, err := engine.Execute(ctx, req, cm)
	if err != nil {
		return nil, err
	}
	var collected []Event
	for {
		select {
		case event, open := <-events:
			if !open {
				return collected, nil
			}
			collected = append(collected, event)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func checkContextStressTerminal(events []Event, answer string, reject bool) error {
	var failures, completions int
	for _, event := range events {
		switch event.Type {
		case EventError:
			failures++
			if !reject || event.Kind != apperrors.KindContextBudget || !event.Done {
				return fmt.Errorf("非预期错误终态：种类=%s 完成=%t 错误=%s", event.Kind, event.Done, event.Error)
			}
		case EventDone:
			completions++
			if reject || event.Content != answer {
				return errors.New("超限返回成功或回答串线")
			}
		case EventAnswer:
			if reject {
				return errors.New("超限仍输出回答")
			}
		case EventInterrupt:
			return errors.New("直接问答非预期中断")
		}
	}
	if (reject && (failures != 1 || completions != 0)) || (!reject && (failures != 0 || completions != 1)) {
		return fmt.Errorf("终态次数异常：错误=%d 成功=%d", failures, completions)
	}
	return nil
}

func TestContextStabilitySequentialConversation(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_STRESS_TEST") != "1" {
		t.Skip("设置 SOLVIFY_CONTEXT_STRESS_TEST=1 执行120轮连续会话压力测试")
	}
	const rounds = 120
	engine := NewEngine(nil, config.AgentConfig{QuickAgentMaxIterations: 8}).(*Engine)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var history []entity.ChatMessage
	var summaries atomic.Int32
	maxInput := 0
	started := time.Now()
	for round := 0; round < rounds; round++ {
		question := fmt.Sprintf("本轮问题-%03d：保持预算4096及来源doc-stability/chunk-1。", round)
		answer := fmt.Sprintf("本轮回答-%03d：预算4096，来源doc-stability/chunk-1。", round)
		req := Request{Profile: ProfileQuick, UserID: "sequential-user", SessionID: "sequential-session", ModelID: "stability", ModelName: "gpt-4o", MaxContextLength: 8192, Query: question, History: history}
		budget, err := llm.ResolveContextBudget(req.ModelID, req.ModelName, req.MaxContextLength, 4096, true, config.ContextDefaults())
		if err != nil {
			t.Fatal(err)
		}
		previous := ""
		if round > 0 {
			previous = fmt.Sprintf("本轮回答-%03d：", round-1)
		}
		cm := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
			if strings.HasPrefix(input[0].Content, "总结已完成交互") {
				summaries.Add(1)
				return schema.AssistantMessage("预算4096，来源doc-stability/chunk-1。", nil), nil
			}
			used := llm.MessagesTokens(input, req.ModelName)
			maxInput = max(maxInput, used)
			if used > budget.Hard || input[len(input)-1].Content != question {
				return nil, errors.New("模型收到超限输入或当前问题被改写")
			}
			if previous != "" {
				found := false
				for _, message := range input {
					found = found || strings.HasPrefix(message.Content, previous)
				}
				if !found {
					return nil, errors.New("最新完成回答丢失")
				}
			}
			return schema.AssistantMessage(answer, nil), nil
		}}
		original, _ := json.Marshal(history)
		events, err := collectContextStressEvents(ctx, engine, req, cm)
		if err == nil {
			err = checkContextStressTerminal(events, answer, false)
		}
		if err != nil {
			t.Fatalf("轮次=%d：%v", round, err)
		}
		after, _ := json.Marshal(history)
		if string(original) != string(after) {
			t.Fatal("历史持久化输入被就地修改")
		}
		history = append(history, entity.ChatMessage{Role: "user", Content: question}, entity.ChatMessage{Role: "assistant", Content: answer + strings.Repeat(" record: alpha=7;", 12)})
	}
	if summaries.Load() == 0 {
		t.Fatal("长会话未触发摘要，用例未覆盖高水位")
	}
	t.Logf("连续轮次=%d 历史消息=%d 摘要次数=%d 最大实际模型输入=%d 耗时=%s", rounds, len(history), summaries.Load(), maxInput, time.Since(started))
}

func TestContextStabilityThousandRoundHistory(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_STRESS_TEST") != "1" {
		t.Skip("设置 SOLVIFY_CONTEXT_STRESS_TEST=1 执行1000轮历史压力测试")
	}
	const rounds = 1000
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		t.Run(string(profile), func(t *testing.T) {
			engine := NewEngine(nil, config.AgentConfig{QuickAgentMaxIterations: 8}).(*Engine)
			req := Request{Profile: profile, UserID: "bulk-user", SessionID: "bulk-session", ModelID: "bulk", ModelName: "gpt-4o", MaxContextLength: 8192, Query: "当前问题：使用最新记录与证据doc-bulk/chunk-1。"}
			for round := 0; round < rounds; round++ {
				req.History = append(req.History,
					entity.ChatMessage{Role: "user", Content: fmt.Sprintf("历史问题-%04d：预算4096，证据doc-bulk/chunk-1。", round)},
					entity.ChatMessage{Role: "assistant", Content: fmt.Sprintf("历史回答-%04d：预算4096。", round)})
			}
			before := llm.MessagesTokens(buildInputMessages(req.Query, req.History), req.ModelName)
			budget, err := llm.ResolveContextBudget(req.ModelID, req.ModelName, req.MaxContextLength, 4096, profile == ProfileQuick, config.ContextDefaults())
			if err != nil || before <= budget.Hard {
				t.Fatal("大量历史未超过硬上限", err)
			}
			var calls atomic.Int32
			var actualInput atomic.Int32
			cm := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
				if strings.HasPrefix(input[0].Content, "总结已完成交互") {
					return schema.AssistantMessage("历史预算4096，证据doc-bulk/chunk-1。", nil), nil
				}
				calls.Add(1)
				actualInput.Store(int32(llm.MessagesTokens(input, req.ModelName)))
				if int(actualInput.Load()) > budget.Hard || input[len(input)-1].Content != req.Query {
					return nil, errors.New("大量历史降级后仍超限或丢失当前问题")
				}
				latest := false
				for _, message := range input {
					latest = latest || strings.HasPrefix(message.Content, "历史回答-0999：")
				}
				if !latest {
					return nil, errors.New("大量历史降级丢失最新回答")
				}
				return schema.AssistantMessage("预算4096，证据doc-bulk/chunk-1。", nil), nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			original, _ := json.Marshal(req.History)
			started := time.Now()
			events, err := collectContextStressEvents(ctx, engine, req, cm)
			if err == nil {
				err = checkContextStressTerminal(events, "预算4096，证据doc-bulk/chunk-1。", false)
			}
			if err != nil || calls.Load() != 1 {
				t.Fatalf("大量历史验收失败：%v 模型调用=%d", err, calls.Load())
			}
			after, _ := json.Marshal(req.History)
			if string(original) != string(after) {
				t.Fatal("大量历史原文被就地修改")
			}
			t.Logf("历史轮次=%d 消息数=%d 压缩前=%d 实际模型输入=%d 硬上限=%d 耗时=%s", rounds, len(req.History), before, actualInput.Load(), budget.Hard, time.Since(started))
		})
	}
}

func TestContextStabilityConcurrentIsolationAndOverflow(t *testing.T) {
	const workers, requestsPerWorker = 24, 8
	engine := NewEngine(nil, config.AgentConfig{QuickAgentMaxIterations: 8}).(*Engine)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	var accepted, rejected, summaries atomic.Int32
	started := time.Now()
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			owner := fmt.Sprintf("owner-%02d", worker)
			for request := 0; request < requestsPerWorker; request++ {
				marker := fmt.Sprintf("%s-request-%02d", owner, request)
				reject := request%3 == 0
				query := marker
				if reject {
					query += strings.Repeat("不可裁剪的当前问题\n", 2000)
				}
				req := Request{Profile: ProfileQuick, UserID: owner, SessionID: owner, ModelID: "concurrent", ModelName: "gpt-4o", MaxContextLength: 8192, Query: query,
					History: []entity.ChatMessage{{Role: "user", Content: marker + strings.Repeat(" record: alpha=7;", 500)}, {Role: "assistant", Content: marker + " 旧答案"}, {Role: "user", Content: marker + " 最近问题"}, {Role: "assistant", Content: marker + " 最近答案"}}}
				var modelCalls atomic.Int32
				cm := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
					for _, message := range input {
						for other := 0; other < workers; other++ {
							if other != worker && strings.Contains(message.Content, fmt.Sprintf("owner-%02d", other)) {
								return nil, errors.New("用户上下文串线")
							}
						}
					}
					if strings.HasPrefix(input[0].Content, "总结已完成交互") {
						summaries.Add(1)
						return schema.AssistantMessage(marker+" 历史摘要", nil), nil
					}
					modelCalls.Add(1)
					if input[len(input)-1].Content != query {
						return nil, errors.New("并发请求的当前问题丢失")
					}
					return schema.AssistantMessage(marker, nil), nil
				}}
				events, err := collectContextStressEvents(ctx, engine, req, cm)
				if err == nil {
					err = checkContextStressTerminal(events, marker, reject)
				}
				wantCalls := int32(1)
				if reject {
					wantCalls = 0
				}
				if err != nil || modelCalls.Load() != wantCalls {
					results <- fmt.Errorf("%s：%v 模型调用=%d 期望=%d", marker, err, modelCalls.Load(), wantCalls)
					return
				}
				if reject {
					rejected.Add(1)
				} else {
					accepted.Add(1)
				}
			}
			results <- nil
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if accepted.Load() != 120 || rejected.Load() != 72 || summaries.Load() == 0 {
		t.Fatalf("并发规模或摘要未覆盖：成功=%d 拒绝=%d 摘要=%d", accepted.Load(), rejected.Load(), summaries.Load())
	}
	t.Logf("并发=%d 总请求=%d 成功=%d 正确拒绝=%d 摘要=%d 耗时=%s", workers, workers*requestsPerWorker, accepted.Load(), rejected.Load(), summaries.Load(), time.Since(started))
}
