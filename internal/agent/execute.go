package agent

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// Execute 执行 Agent 推理循环
//
// Agent 自主决定工具调用时机：knowledge_search / web_search
// 不做预检索，所有信息获取由 LLM 通过工具调用完成
func (e *Engine) Execute(ctx context.Context, req Request) (<-chan Event, error) {
	eventCh := make(chan Event, 100)
	go func() {
		defer close(eventCh)
		e.reActLoop(ctx, req, eventCh)
	}()
	return eventCh, nil
}

// toHistoryMessages 将实体历史消息转换为内部格式
func toHistoryMessages(history []entity.ChatMessage) []historyMessage {
	msgs := make([]historyMessage, 0, len(history))
	for _, msg := range history {
		msgs = append(msgs, historyMessage{
			role:    msg.Role,
			content: msg.Content,
		})
	}
	return msgs
}
