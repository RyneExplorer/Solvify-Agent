package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "solvify-agent/internal/model/dto/response"
)

func TestChatRoutes_ContextOverflowStreamTerminates(t *testing.T) {
	for _, mode := range []string{"quick", "smart-reasoning"} {
		t.Run(mode, func(t *testing.T) {
			events := make(chan dto.StreamEvent, 1)
			events <- dto.StreamEvent{Type: "error", Title: "上下文预算不足", Detail: "必要内容超过模型输入额度，请缩短问题或选择更大窗口的模型", Status: "error", Done: true, Retryable: false}
			close(events)
			body := `{"content":"当前问题","knowledge_base_ids":["kb"],"model_id":"model","model_type":"system","search_mode":"` + mode + `"}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions/"+testSessionID+"/messages", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			writer := streamRecorder{httptest.NewRecorder()}
			newTestRouter(&stubChatService{stream: events}, 1).ServeHTTP(writer, request)
			if writer.Code != http.StatusOK || !strings.HasPrefix(writer.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatal("超限改变既有SSE响应契约")
			}
			frames := strings.Split(strings.TrimSpace(writer.Body.String()), "\n\n")
			if len(frames) != 1 || !strings.HasPrefix(frames[0], "data: ") {
				t.Fatalf("超限终态丢失或重复：%s", writer.Body.String())
			}
			var event dto.StreamEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[0], "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "error" || !event.Done || event.Retryable || !strings.Contains(event.Title, "上下文") || event.Content != "" {
				t.Fatalf("超限未正确传递失败终态：%+v", event)
			}
			if !strings.Contains(frames[0], `"retryable":false`) {
				t.Fatal("缺少禁止自动重试的显式标志")
			}
		})
	}
}
