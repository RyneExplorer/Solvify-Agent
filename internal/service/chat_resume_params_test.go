package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"solvify-agent/internal/model/entity"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/repository"

	"solvify-agent/internal/agent"
)

// ── applyResumeParams 守卫 ──
//
// 抽这个函数的理由就是「原来测不了」：它不产生事件、只改agentReq 的两个字段，
// 混在 processAgentMode 里既读不出来也没法造场景。三条分支各会犯不同的错：
//   - 忘了判超时 → 拿已被 Ticker 清理的 checkpoint 去恢复，必然失败且报错，看不懂
//   - 忘了判 Content=="" → 前端刷新页面后带着空消息来，恢复被静默跳过
//   - clarify 与 danger 的 ResumeData 形态搞混 → 中间件解析失败，恢复永远进不去

// fakeSessionRepo 只实现 applyResumeParams 会碰到的三个方法，
// 其余方法 panic —— 一旦生产代码新增调用，测试立刻炸而不是静默返回零值。
type fakeSessionRepo struct {
	repository.ChatSessionRepo // 嵌入接口，未实现的方法被调用即 panic

	session    *entity.ChatSession
	findErr    error
	clearCpErr error
	clearClErr error

	clearedCheckpoint bool
	clearedClarify    bool
}

func (f *fakeSessionRepo) FindByID(context.Context, string) (*entity.ChatSession, error) {
	return f.session, f.findErr
}
func (f *fakeSessionRepo) ClearPendingCheckpoint(context.Context, string) error {
	f.clearedCheckpoint = true
	return f.clearCpErr
}
func (f *fakeSessionRepo) ClearPendingClarify(context.Context, string) error {
	f.clearedClarify = true
	return f.clearClErr
}

// pendingSession 造一个带 pending checkpoint 的 session。
// age 是 checkpoint 距今多久，clarify 决定是哪种中断。
func pendingSession(age time.Duration, clarify bool) *entity.ChatSession {
	raw, _ := json.Marshal(entity.PendingCheckpointData{
		CheckpointID: "cp-1",
		InterruptID:  "int-1",
		IsClarify:clarify,
		SetAt:        time.Now().Add(-age),
	})
	return &entity.ChatSession{ID: "s1", PendingCheckpoint: raw}
}

func newResumeTestService(repo repository.ChatSessionRepo) *chatService {
	return &chatService{sessionRepo: repo}
}

func sendReq(content string) requestdto.SendMessageRequest {
	return requestdto.SendMessageRequest{Content: content}
}

// TestApplyResumeSkipsWhenNoPending 正常首轮：不得写任何恢复参数，也不得清标记。
func TestApplyResumeSkipsWhenNoPending(t *testing.T) {
	repo := &fakeSessionRepo{session: &entity.ChatSession{ID: "s1"}}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq("你好"), &agentReq)

	if agentReq.CheckpointID != "" || agentReq.ResumeData != nil {
		t.Fatalf("无 pending checkpoint 时却写了恢复参数: cp=%q resume=%v", agentReq.CheckpointID, agentReq.ResumeData)
	}
	if repo.clearedCheckpoint || repo.clearedClarify {
		t.Fatal("无 pending 时不该清标记（会把别的会话状态抹掉）")
	}
}

// TestApplyResumeSkipsWhenSessionMissing session 查不到（nil）时按首轮执行，
// 但绝不能清标记 —— 那属于别的会话的状态。
func TestApplyResumeSkipsWhenSessionMissing(t *testing.T) {
	repo := &fakeSessionRepo{session: nil}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq("你好"), &agentReq)

	if agentReq.CheckpointID != "" {
		t.Fatalf("session 为 nil 时却恢复了: %q", agentReq.CheckpointID)
	}
	if repo.clearedCheckpoint || repo.clearedClarify {
		t.Fatal("session 为 nil 时不该清标记")
	}
}

// TestApplyResumeClarifyUsesRawUserAnswer clarify 的 ResumeData 必须是用户回答原文（字符串）。
// 中间件按 FollowUpTool 的 UserAnswer 语义解析，传结构体就解析不出来。
func TestApplyResumeClarifyUsesRawUserAnswer(t *testing.T) {
	repo := &fakeSessionRepo{session: pendingSession(time.Minute, true)}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq("北京"), &agentReq)

	if agentReq.CheckpointID != "cp-1" {
		t.Fatalf("CheckpointID=%q 期望 cp-1", agentReq.CheckpointID)
	}
	got, ok := agentReq.ResumeData["int-1"]
	if !ok {
		t.Fatalf("ResumeData 键错: %v", agentReq.ResumeData)
	}
	if s, isStr := got.(string); !isStr || s != "北京" {
		t.Fatalf("clarify 恢复数据=%v(%T)，期望原始字符串", got, got)
	}
	if repo.clearedCheckpoint {
		t.Fatal("正常恢复路径不该清标记（后面 emitDoneAndSave 成功后还要清）")
	}
}

// TestApplyResumeDangerUsesApprovalResult danger 的 ResumeData 必须是结构化 ApprovalResult，
// 且要把前端的中文「同意」映射成 Approved=true。
func TestApplyResumeDangerUsesApprovalResult(t *testing.T) {
	repo := &fakeSessionRepo{session: pendingSession(time.Minute, false)}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq("同意"), &agentReq)

	if agentReq.CheckpointID != "cp-1" {
		t.Fatalf("CheckpointID=%q 期望 cp-1", agentReq.CheckpointID)
	}
	got, ok := agentReq.ResumeData["int-1"]
	if !ok {
		t.Fatalf("ResumeData 键错: %v", agentReq.ResumeData)
	}
	ap, isPtr := got.(*agent.ApprovalResult)
	if !isPtr {
		t.Fatalf("danger 恢复数据=%T 期望 *agent.ApprovalResult（前端发的是中文文本，必须经parseApprovalResult 映射）", got)
	}
	if !ap.Approved {
		t.Fatalf("「同意」应映射为 Approved=true，实际 %+v", ap)
	}
}

// TestApplyResumeTimesOut 超过阈值要放弃恢复**并清标记**。
// 三个子场景各钉一个阈值边界：clarify 10min、danger 24h。
func TestApplyResumeTimesOut(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		clarify bool
	}{
		{"clarify 超 10min", 11 * time.Minute, true},
		{"clarify 未到 10min", 9 * time.Minute, true},
		{"danger 超 24h", 25 * time.Hour, false},
		{"danger 未到 24h", 23 * time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := &fakeSessionRepo{session: pendingSession(c.age, c.clarify)}
			var agentReq agent.Request
			svc := newResumeTestService(repo)
			svc.applyResumeParams(context.Background(), "s1", sendReq("同意"), &agentReq)

			expired := c.name == "clarify 超 10min" || c.name == "danger 超 24h"
			if expired {
				if agentReq.CheckpointID != "" {
					t.Fatalf("已超时的 checkpoint 不该恢复: %q", agentReq.CheckpointID)
				}
				if !repo.clearedCheckpoint || !repo.clearedClarify {
					t.Fatalf("超时应清两处标记，实际 clearedCp=%v clearedCl=%v", repo.clearedCheckpoint, repo.clearedClarify)
				}
				return
			}
			if agentReq.CheckpointID != "cp-1" {
				t.Fatalf("未超时应恢复，实际 CheckpointID=%q", agentReq.CheckpointID)
			}
			if repo.clearedCheckpoint {
				t.Fatal("未超时不该清标记")
			}
		})
	}
}

// TestApplyResumeWithoutUserContent 有 checkpoint 但用户这次没带内容（前端刷新页面重开）
// → 走首轮，并清掉标记。
func TestApplyResumeWithoutUserContent(t *testing.T) {
	repo := &fakeSessionRepo{session: pendingSession(time.Minute, false)}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq(""), &agentReq)

	if agentReq.CheckpointID != "" || agentReq.ResumeData != nil {
		t.Fatalf("无内容却写了恢复参数: cp=%q resume=%v", agentReq.CheckpointID, agentReq.ResumeData)
	}
	if !repo.clearedCheckpoint || !repo.clearedClarify {
		t.Fatalf("应清两处标记，实际 clearedCp=%v clearedCl=%v", repo.clearedCheckpoint, repo.clearedClarify)
	}
}

// TestApplyResumeFindErrorDoesNotTouchAnything FindByID 出错时不得清标记。
// 退化形态：把 err 当成「无 pending」顺手清理 ⇒ 数据库一抖就把用户没审批完的状态抹了。
func TestApplyResumeFindErrorDoesNotTouchAnything(t *testing.T) {
	repo := &fakeSessionRepo{session: pendingSession(time.Minute, false), findErr: errors.New("db down")}
	var agentReq agent.Request
	svc := newResumeTestService(repo)
	svc.applyResumeParams(context.Background(), "s1", sendReq("同意"), &agentReq)

	if agentReq.CheckpointID != "" {
		t.Fatalf("读取失败时不该恢复: %q", agentReq.CheckpointID)
	}
	if repo.clearedCheckpoint || repo.clearedClarify {
		t.Fatal("读取失败时清标记＝把用户没审批完的状态抹掉")
	}
}

// TestParseApprovalResultMapping 单独钉住中文审批文本到结构体的映射表。
// applyResumeParams 的 danger 分支完全依赖它，映射错了审批会被当成拒绝。
func TestParseApprovalResultMapping(t *testing.T) {
	approved := []string{"approve", "同意", "确认", "yes", "y", "ok", `{"approved":true}`}
	for _, in := range approved {
		if got := parseApprovalResult(in); !got.Approved {
			t.Errorf("parseApprovalResult(%q).Approved=false，期望 true", in)
		}
	}
	rejected := []string{"reject", "拒绝", "取消", "no", "n", "", "随便什么"}
	for _, in := range rejected {
		if got := parseApprovalResult(in); got.Approved {
			t.Errorf("parseApprovalResult(%q).Approved=true，期望 false（默认必须拒绝）", in)
		}
	}
	if got := parseApprovalResult(`{"approved":true,"disapprove_reason":"x"}`); !got.Approved {
		t.Error("结构化 JSON 的 approved=true 未被识别")
	}
	if got := parseApprovalResult(""); got.DisapproveReason == nil || *got.DisapproveReason == "" {
		t.Error("空输入应给出拒绝原因，便于排查")
	}
}