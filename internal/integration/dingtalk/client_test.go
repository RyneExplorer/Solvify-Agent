package dingtalk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"solvify-agent/pkg/config"
)

// TestClientListNodesUsesHeaderToken 验证节点列表使用 Header 鉴权和分页参数
func TestClientListNodesUsesHeaderToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token-1","expires_in":7200}`))
		case "/v2.0/wiki/nodes":
			if r.Header.Get("x-acs-dingtalk-access-token") != "token-1" {
				t.Fatalf("未使用钉钉 Header 鉴权")
			}
			if r.URL.Query().Get("parentNodeId") != "root-1" || r.URL.Query().Get("nextToken") != "next-1" {
				t.Fatalf("节点列表分页参数错误: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"nodes":[{"nodeId":"node-1","workspaceId":"ws-1","name":"a.md","size":12,"type":"FILE"}],"nextToken":"next-2"}`))
		default:
			t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(config.DingTalkConfig{AppKey: "app-key", AppSecret: "app-secret"})
	client.httpClient = server.Client()
	client.accessTokenURL = server.URL + "/gettoken"
	client.apiBaseURL = server.URL

	nodes, nextToken, err := client.ListNodes(context.Background(), "union-1", "root-1", "next-1", 50)
	if err != nil {
		t.Fatalf("获取节点列表失败: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-1" || nextToken != "next-2" {
		t.Fatalf("节点列表响应解析错误: nodes=%v next=%s", nodes, nextToken)
	}
}

// TestClientQueryDentryIDEscapesPath 验证 dentryUuid 路径参数会转义
func TestClientQueryDentryIDEscapesPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token-1","expires_in":7200}`))
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
	client.accessTokenURL = server.URL + "/gettoken"
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
		case "/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token-1","expires_in":7200}`))
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
	client.accessTokenURL = server.URL + "/gettoken"
	client.apiBaseURL = server.URL

	data, hash, err := client.DownloadFile(context.Background(), "union-1", "s-1", "d-1")
	if err != nil {
		t.Fatalf("下载文件失败: %v", err)
	}
	if string(data) != "hello" || hash == "" {
		t.Fatalf("下载内容解析错误: data=%q hash=%s", string(data), hash)
	}
}

// serverURL 从测试请求还原服务地址
func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
