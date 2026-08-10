package tool

import (
	"context"
	"fmt"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
)

// DeleteDocumentInput 危险工具 delete_document 的参数
type DeleteDocumentInput struct {
	DocumentID string `json:"document_id" jsonschema:"required" jsonschema_description:"要删除的文档 ID"`
	Reason     string `json:"reason" jsonschema:"required" jsonschema_description:"删除原因（审批时展示给用户）"`
}

// runDeleteDocument 执行软删除（业务逻辑，纯函数风格）
// 注意：审批拦截由 agent.tool_middleware.go 的 InvokableToolMiddleware 统一处理，
// 本函数只负责参数校验 → 文档存在性检查 → 软删除 → 返回结果。
func runDeleteDocument(ctx context.Context, documentRepo repository.DocumentRepository, userID string, input DeleteDocumentInput) (ToolResponse, error) {
	// 文档存在性校验
	doc, found, err := documentRepo.FindByID(ctx, userID, input.DocumentID, 5)
	if err != nil {
		logger.Errorf("[Tool] delete_document 查询异常: docID=%s, err=%v", input.DocumentID, err)
		return ToolResponse{Success: false, Message: fmt.Sprintf("查询文档失败: %v", err)}, nil
	}
	if !found {
		return ToolResponse{Success: false, Message: fmt.Sprintf("未找到文档 %s，可能已被删除", input.DocumentID)}, nil
	}

	// 软删除：设置 deleted_at，保留 30 天
	deletedAt := time.Now()
	expiredAt := deletedAt.Add(30 * 24 * time.Hour)
	ok, err := documentRepo.SoftDelete(ctx, userID, input.DocumentID, 5, 0, deletedAt, expiredAt)
	if err != nil {
		logger.Errorf("[Tool] delete_document 软删除失败: docID=%s, err=%v", input.DocumentID, err)
		return ToolResponse{Success: false, Message: fmt.Sprintf("删除失败: %v", err)}, nil
	}
	if !ok {
		return ToolResponse{Success: false, Message: fmt.Sprintf("文档 %s 删除失败（可能已删除或无权访问）", input.DocumentID)}, nil
	}

	logger.Infof("[Tool] delete_document 完成: docID=%s, title=%q, reason=%s", input.DocumentID, doc.Title, input.Reason)

	return ToolResponse{Success: true, Message: fmt.Sprintf("✅ 文档「%s」已删除", doc.Title), Data: map[string]any{
		"document_id": input.DocumentID,
		"title":       doc.Title,
		"filename":    doc.FileName,
		"reason":      input.Reason,
		"approved":    true,
	}}, nil
}

// NewDeleteDocumentTool 创建 delete_document 工具的构建函数。
// 返回值是一个函数：接收 userID, kbIDs → 返回 InvokableTool（可直接当 BaseTool 用）
func NewDeleteDocumentTool(documentRepo repository.DocumentRepository) func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[DeleteDocumentInput, ToolResponse](
			"delete_document",
			"删除指定知识库文档（软删除，保留 30 天）。危险操作：调用会被自动拦截等待用户审批，无需重复提示用户确认。",
			func(ctx context.Context, input DeleteDocumentInput) (ToolResponse, error) {
				return runDeleteDocument(ctx, documentRepo, userID, input)
			},
		)
		return t
	}
}
