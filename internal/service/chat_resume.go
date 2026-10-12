package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"solvify-agent/internal/agent"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/logger"
)

// applyResumeParams 判断本次请求要不要恢复上次中断的执行，是则把恢复参数写进 agentReq。
//
// 三种情况都表示「本次走首次执行」，不改 agentReq：
//   - session 上没有 pending checkpoint（正常首轮）
//   - checkpoint 已超时（字节可能已被后台 Ticker 清理，恢复必然失败）
//   - 有 checkpoint 但用户这次没带审批内容（前端重开了页面）
//
// 抽出来的原因是这段判断原本埋在事件消费循环上方：不产生事件、只改两个字段，
// 混在里面既读不出来也测不了 —— 抽出后可覆盖上面三种分支 + 两种 ResumeData 形态。
func (s *chatService) applyResumeParams(ctx context.Context, sessionID string, req requestdto.SendMessageRequest, agentReq *agent.Request) error {
	session, err := s.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		// 查不到 session 时按「无 pending」继续，会让用户以为审批已提交、系统却重跑一遍。
		// 至少留下 sessionID 便于对账。
		logger.Warnf("[ChatService] 读取 session 失败，无法判断是否有待恢复 checkpoint: sessionID=%s, err=%v", sessionID, err)
		return fmt.Errorf("读取待恢复状态失败: %w", err)
	}
	if session == nil {
		return nil
	}
	pc, err := session.GetPendingCheckpoint()
	if err != nil {
		return fmt.Errorf("解析待恢复状态失败: %w", err)
	}
	if pc == nil {
		return nil
	}

	// clarify 等的是「用户回答问题」，10min 够了；danger 等的是「用户做审批决策」，24h。
	// 两个阈值混用会把还在慢慢审的请求当成过期直接清掉。
	timeout := entity.CheckpointDefaultTimeout
	if pc.IsClarify {
		timeout = entity.ClarifyDefaultTimeout
	}
	if pc.IsExpired(timeout) {
		logger.Warnf("[ChatService] pending checkpoint 已超时（%v），放弃恢复并清除: checkpointID=%s", timeout, pc.CheckpointID)
		s.clearPendingResume(ctx, sessionID)
		return nil
	}

	if req.Content == "" {
		logger.Warnf("[ChatService] 有 pending checkpoint 但用户未提供审批内容，走首次执行: checkpointID=%s", pc.CheckpointID)
		s.clearPendingResume(ctx, sessionID)
		return nil
	}

	logger.Infof("[ChatService] 设置恢复参数: checkpointID=%s, interruptID=%s, isClarify=%v", pc.CheckpointID, pc.InterruptID, pc.IsClarify)
	agentReq.CheckpointID = pc.CheckpointID
	if pc.IsClarify {
		// clarify 的恢复数据是用户的回答文本，与官方 FollowUpTool 的 UserAnswer 语义一致。
		agentReq.ResumeData = map[string]any{pc.InterruptID: req.Content}
		return nil
	}
	// danger 的恢复数据必须是结构化 ApprovalResult —— 前端发的是「同意/拒绝」这类中文文本。
	agentReq.ResumeData = map[string]any{pc.InterruptID: parseApprovalResult(req.Content)}
	return nil
}

// clearPendingResume 清掉待恢复标记。checkpoint 与 clarify 两处标记成对写入，必须成对清除。
func (s *chatService) clearPendingResume(ctx context.Context, sessionID string) {
	if err := s.sessionRepo.ClearPendingCheckpoint(ctx, sessionID); err != nil {
		logger.Warnf("[ChatService] 清除 pending checkpoint 失败: sessionID=%s, err=%v", sessionID, err)
	}
	if err := s.sessionRepo.ClearPendingClarify(ctx, sessionID); err != nil {
		logger.Warnf("[ChatService] 清除 pending clarify 失败: sessionID=%s, err=%v", sessionID, err)
	}
}

// parseApprovalResult 将前端返回的审批文本映射为官方结构化 ApprovalResult。
// 这是 Host 的职责：官方 Host 直接发送 ApprovalResult，本项目前端目前发送"同意/拒绝"等文本，
// 这里做一层等价映射，使中间件可沿用官方 approval_wrapper.go 的 *ApprovalResult 数据契约。
// 同时兼容官方风格的 JSON：{"approved":true,"disapprove_reason":"..."}（前端若直接发结构化体也支持）。
func parseApprovalResult(content string) *agent.ApprovalResult {
	content = strings.TrimSpace(content)
	if content == "" {
		reason := "未提供审批内容"
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &reason}
	}
	// 官方风格的结构化 JSON
	var structured agent.ApprovalResult
	if json.Unmarshal([]byte(content), &structured) == nil && (structured.Approved || structured.DisapproveReason != nil) {
		return &structured
	}
	// 兼容旧前端：关键词匹配
	switch strings.ToLower(content) {
	case "approve", "同意", "确认", "yes", "y", "ok":
		return &agent.ApprovalResult{Approved: true}
	case "reject", "拒绝", "取消", "no", "n":
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &content}
	default:
		// 无法识别：默认拒绝，原始文本作为原因便于排查
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &content}
	}
}
