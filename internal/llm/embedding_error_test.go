package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddingHTTPFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  bool
		code   string
	}{{400, false, "configuration"}, {401, false, "authentication"}, {408, true, "unavailable"}, {429, true, "unavailable"}, {503, true, "unavailable"}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"error":{"message":"test-secret-private-content","type":"test_error"}}`)
			}))
			defer server.Close()
			client, err := NewEmbeddingClient(context.Background(), EmbeddingClientConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "test", Dimension: 2, Timeout: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Embed(context.Background(), "私有文本")
			var classified *EmbeddingError
			if !errors.As(err, &classified) || classified.Code != tc.code || classified.Retryable() != tc.retry {
				t.Fatalf("错误分类不正确: %v", err)
			}
			if strings.Contains(err.Error(), "test-secret") {
				t.Fatal("泄露上游响应")
			}
		})
	}
}
