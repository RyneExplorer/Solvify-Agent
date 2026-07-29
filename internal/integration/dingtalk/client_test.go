package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"solvify-agent/pkg/config"
)

// TestNodeUnmarshalModifiedTimeFormats 验证节点更新时间兼容时间戳和分钟精度时间
func TestNodeUnmarshalModifiedTimeFormats(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected int64
	}{
		{name: "毫秒时间戳", value: `"1719999999000"`, expected: 1719999999000},
		{name: "分钟精度时间", value: `"2026-07-01T19:22Z"`, expected: time.Date(2026, 7, 1, 19, 22, 0, 0, time.UTC).Unix()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var node Node
			if err := json.Unmarshal([]byte(`{"modifiedTime":`+tt.value+`}`), &node); err != nil {
				t.Fatalf("解析节点更新时间失败: %v", err)
			}
			if node.ModifiedAt != tt.expected {
				t.Fatalf("节点更新时间不符合预期: got=%d want=%d", node.ModifiedAt, tt.expected)
			}
		})
	}
}

// TestClientListNodesUsesHeaderToken 验证节点列表使用 Header 鉴权和分页参数
func TestClientListNodesUsesHeaderToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			if r.Method != http.MethodPost {
				t.Fatalf("accessToken 请求方法错误: %s", r.Method)
			}
			if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				t.Fatalf("accessToken 请求体类型错误")
			}
			_, _ = w.Write([]byte(`{"accessToken":"token-1","expireIn":7200}`))
		case "/v2.0/wiki/nodes":
			if r.Header.Get("x-acs-dingtalk-access-token") != "token-1" {
				t.Fatalf("未使用钉钉 Header 鉴权")
			}
			if r.URL.Query().Get("parentNodeId") != "root-1" || r.URL.Query().Get("nextToken") != "next-1" {
				t.Fatalf("节点列表分页参数错误: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"node-1","workspaceId":"ws-1","name":"a.md","size":"12","type":"FILE","modifiedTime":"1719999999000"}],"nextToken":"next-2"}`))
		default:
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.accessTokenURL = server.URL + "/v1.0/oauth2/accessToken"
	client.apiBaseURL = server.URL

	nodes, nextToken, err := client.ListNodes(context.Background(), "union-1", "root-1", "next-1", 50)
	if err != nil {
		t.Fatalf("获取节点列表失败: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-1" || nodes[0].Size != 12 || nodes[0].ModifiedAt != 1719999999000 || nextToken != "next-2" {
		t.Fatalf("节点列表响应解析错误: nodes=%v next=%s", nodes, nextToken)
	}
}

// TestClientQueryDentryIDEscapesPath 验证 dentryUuid 路径参数会转义
func TestClientQueryDentryIDEscapesPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token-1","expireIn":7200}`))
		case "/v2.0/doc/dentries/abc/def/queryDentryId":
			if !strings.Contains(r.URL.RawPath, "abc%2Fdef") && !strings.Contains(r.RequestURI, "abc%2Fdef") {
				t.Fatalf("dentryUuid 未正确转义: %s", r.RequestURI)
			}
			_, _ = w.Write([]byte(`{"dentryUuid":"abc/def","dentryId":"d-1","spaceId":"s-1"}`))
		default:
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.accessTokenURL = server.URL + "/v1.0/oauth2/accessToken"
	client.apiBaseURL = server.URL

	output, err := client.QueryDentryID(context.Background(), "union-1", "abc/def")
	if err != nil {
		t.Fatalf("查询 dentryId 失败: %v", err)
	}
	if output.SpaceID != "s-1" || output.DentryID != "d-1" {
		t.Fatalf("dentryId 响应解析错误: %+v", output)
	}
}

// TestClientDownloadFileUsesReturnedHeaders 验证下载文件使用钉钉返回的签名 Header
func TestClientDownloadFileUsesReturnedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token-1","expireIn":7200}`))
		case "/v1.0/storage/spaces/s-1/dentries/d-1/downloadInfos/query":
			if r.Header.Get("x-acs-dingtalk-access-token") != "token-1" {
				t.Fatalf("下载信息未使用钉钉 Header 鉴权")
			}
			_, _ = w.Write([]byte(`{"protocol":"HEADER_SIGNATURE","headerSignatureInfo":{"resourceUrls":["` + serverURL(r) + `/download"],"headers":{"X-Sign":"ok"}}}`))
		case "/download":
			if r.Header.Get("X-Sign") != "ok" {
				t.Fatalf("文件下载未携带签名 Header")
			}
			_, _ = w.Write([]byte("hello"))
		default:
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.accessTokenURL = server.URL + "/v1.0/oauth2/accessToken"
	client.apiBaseURL = server.URL

	data, hash, err := client.DownloadFile(context.Background(), "union-1", "s-1", "d-1")
	if err != nil {
		t.Fatalf("下载文件失败: %v", err)
	}
	if string(data) != "hello" || hash == "" {
		t.Fatalf("下载内容解析错误: data=%q hash=%s", string(data), hash)
	}
}

// TestClientQueryDocumentBlocks 验证在线文档块元素查询参数和响应
func TestClientQueryDocumentBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"token-1","expireIn":7200}`))
		case "/v1.0/doc/suites/documents/doc-1/blocks":
			if r.Method != http.MethodGet || r.URL.Query().Get("operatorId") != "union-1" {
				t.Fatalf("块元素查询参数错误: %s %s", r.Method, r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"result":{"data":[{"blockType":"paragraph","paragraph":{"text":"正文"}}]},"success":true}`))
		default:
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.accessTokenURL = server.URL + "/v1.0/oauth2/accessToken"
	client.apiBaseURL = server.URL

	blocks, err := client.QueryDocumentBlocks(context.Background(), "union-1", "doc-1")
	if err != nil {
		t.Fatalf("查询在线文档块元素失败: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["blockType"] != "paragraph" {
		t.Fatalf("块元素响应解析错误: %+v", blocks)
	}
}

// serverURL 从测试请求还原服务地址
func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

// TestClientExchangeUserAccessToken 验证扫码授权码使用新版 OAuth JSON 请求兑换用户 token
func TestClientExchangeUserAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth2/userAccessToken" {
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("用户 token 请求方法错误: %s", r.Method)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("用户 token 请求体类型错误")
		}
		_, _ = w.Write([]byte(`{"accessToken":"user-token","refreshToken":"refresh-token","expireIn":7200,"corpId":"corp-1"}`))
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.apiBaseURL = server.URL

	output, err := client.ExchangeUserAccessToken(context.Background(), "auth-code")
	if err != nil {
		t.Fatalf("兑换用户 token 失败: %v", err)
	}
	if output.AccessToken != "user-token" || output.CorpID != "corp-1" {
		t.Fatalf("用户 token 响应解析错误: %+v", output)
	}
}

// TestClientGetCurrentUserInfoUsesUserToken 验证用户信息接口使用个人 token Header
func TestClientGetCurrentUserInfoUsesUserToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/contact/users/me" {
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
		if r.Header.Get("x-acs-dingtalk-access-token") != "user-token" {
			t.Fatalf("用户信息接口未使用个人 token Header")
		}
		_, _ = w.Write([]byte(`{"nick":"张三","avatarUrl":"https://example.com/architecture.png","openId":"open-1","unionId":"union-1","email":"a@example.com"}`))
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.apiBaseURL = server.URL

	output, err := client.GetCurrentUserInfo(context.Background(), "user-token")
	if err != nil {
		t.Fatalf("获取用户信息失败: %v", err)
	}
	if output.UnionID != "union-1" || output.OpenID != "open-1" {
		t.Fatalf("用户信息响应解析错误: %+v", output)
	}
}
