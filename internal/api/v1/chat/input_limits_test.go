package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
)

type inputLimitChatService struct {
	service.ChatServiceInterface
	mutex   sync.Mutex
	calls   int
	content string
}

func (stub *inputLimitChatService) SendMessage(_ context.Context, _, _ string, input requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error) {
	stub.mutex.Lock()
	stub.calls++
	stub.content = input.Content
	stub.mutex.Unlock()
	events := make(chan dto.StreamEvent, 1)
	events <- dto.StreamEvent{Type: "done", Content: "收到", Done: true}
	close(events)
	return events, nil
}

func inputLimitBody(t *testing.T, content, mode string) string {
	t.Helper()
	body, err := json.Marshal(requestdto.SendMessageRequest{Content: content, KnowledgeBaseIDs: []string{"kb"}, SearchMode: mode, ModelID: "model", ModelType: "system"})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestChatRoutes_MessageCharacterLimit(t *testing.T) {
	for _, mode := range []string{"quick", "smart-reasoning"} {
		for _, symbol := range []string{"a", "中", "😀"} {
			for _, length := range []int{requestdto.MaxChatMessageChars, requestdto.MaxChatMessageChars + 1} {
				content := strings.Repeat(symbol, length)
				t.Run(fmt.Sprintf("%s/%s/%d", mode, symbol, length), func(t *testing.T) {
					stub := &inputLimitChatService{}
					req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions/"+testSessionID+"/messages", strings.NewReader(inputLimitBody(t, content, mode)))
					req.Header.Set("Content-Type", "application/json")
					writer := streamRecorder{httptest.NewRecorder()}
					newTestRouter(stub, 1).ServeHTTP(writer, req)
					if length <= requestdto.MaxChatMessageChars {
						if stub.calls != 1 || stub.content != content || !strings.HasPrefix(writer.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(writer.Body.String(), `"type":"done"`) {
							t.Fatal("边界内消息未保持原文进入SSE")
						}
						return
					}
					result := decode(t, writer.ResponseRecorder)
					if writer.Code != http.StatusOK || result.Code != apperrors.CodeBadRequest || !strings.Contains(result.Message, "4000") || stub.calls != 0 {
						t.Fatalf("超长消息未在Service之前拒绝：%+v calls=%d", result, stub.calls)
					}
				})
			}
		}
	}
}

func TestChatRoutes_RequestBodyLimit(t *testing.T) {
	base := inputLimitBody(t, "合法问题", "quick")
	for _, unknownLength := range []bool{false, true} {
		for _, size := range []int{maxChatRequestBytes, maxChatRequestBytes + 1} {
			t.Run(fmt.Sprintf("bytes=%d/unknown=%t", size, unknownLength), func(t *testing.T) {
				stub := &inputLimitChatService{}
				body := base + strings.Repeat(" ", size-len(base))
				req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions/"+testSessionID+"/messages", strings.NewReader(body))
				if unknownLength {
					req.ContentLength = -1
				}
				req.Header.Set("Content-Type", "application/json")
				writer := streamRecorder{httptest.NewRecorder()}
				newTestRouter(stub, 1).ServeHTTP(writer, req)
				if size <= maxChatRequestBytes {
					if stub.calls != 1 || stub.content != "合法问题" {
						t.Fatal("请求体边界内未放行原文")
					}
					return
				}
				result := decode(t, writer.ResponseRecorder)
				if result.Code != apperrors.CodeBadRequest || !strings.Contains(result.Message, "64KiB") || stub.calls != 0 {
					t.Fatalf("超限请求体未在业务之前拒绝：%+v calls=%d", result, stub.calls)
				}
			})
		}
	}
}

func TestChatHTTP_FormerRound12RejectedThenContinue(t *testing.T) {
	stub := &inputLimitChatService{}
	server := httptest.NewServer(newTestRouter(stub, 1))
	defer server.Close()
	endpoint := server.URL + "/api/v1/chat/sessions/" + testSessionID + "/messages"
	oversized := "下面是不可省略的当前问题，完整读入后只回答收到。\n" + strings.Repeat("当前问题必要数据 alpha=7 budget-check=overflow\n", 1500)
	response, err := server.Client().Post(endpoint, "application/json", strings.NewReader(inputLimitBody(t, oversized, "quick")))
	if err != nil {
		t.Fatal(err)
	}
	var rejection envelope
	decodeErr := json.NewDecoder(response.Body).Decode(&rejection)
	response.Body.Close()
	stub.mutex.Lock()
	calls := stub.calls
	stub.mutex.Unlock()
	if decodeErr != nil || rejection.Code != apperrors.CodeBadRequest || calls != 0 || strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("原第12轮样本未在HTTP入口拒绝：%+v calls=%d err=%v", rejection, calls, decodeErr)
	}
	response, err = server.Client().Post(endpoint, "application/json", strings.NewReader(inputLimitBody(t, "现在正常继续", "quick")))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	stub.mutex.Lock()
	calls, content := stub.calls, stub.content
	stub.mutex.Unlock()
	if readErr != nil || !strings.Contains(string(body), `"type":"done"`) || calls != 1 || content != "现在正常继续" {
		t.Fatalf("入口拒绝影响后续合法请求：%s calls=%d err=%v", body, calls, readErr)
	}
}
