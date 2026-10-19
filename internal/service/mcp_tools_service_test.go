package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
)

func TestMCPUserSourcesAndEffectiveState(t *testing.T) {
	template := "template"
	repo := &mcpMemoryRepo{rows: map[string]entity.MCPConnection{
		"custom": {ID: "custom", UserID: "u", IsEnabled: true, Config: []byte(`{}`), Connection: []byte(`{"transport":"http","url":"https://example.com/mcp"}`)},
		"system": {ID: "system", UserID: "u", TemplateID: &template, Template: &entity.MCPTemplate{Name: "系统搜索", IsEnabled: false}, IsEnabled: true, Config: []byte(`{"api_key":"secret"}`)},
	}}
	s := NewMCPService(repo, nil, nil)
	rows, err := s.List(context.Background(), "u")
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "custom" && (row.Source != "custom" || !row.EffectiveEnabled) {
			t.Fatal("自定义来源错误")
		}
		if row.ID == "system" && (row.Source != "system" || row.TemplateName != "系统搜索" || row.TemplateEnabled || row.EffectiveEnabled || !row.IsEnabled) {
			t.Fatal("管理员禁用没有覆盖本人偏好")
		}
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "secret") {
		t.Fatal("来源列表泄露凭据")
	}
}

func TestMCPToolCannotBypassDisabledSystemTemplate(t *testing.T) {
	ctx := context.Background()
	id := "template"
	p := &mcpTemplateRepo{p: entity.MCPTemplate{ID: id, IsEnabled: false, Connection: []byte(`{"transport":"http","url":"https://example.com/mcp"}`)}}
	r := &mcpMemoryRepo{rows: map[string]entity.MCPConnection{"c": {ID: "c", UserID: "u", TemplateID: &id, DisplayName: "系统搜索", Config: []byte(`{"disabled_tools":["fetch"]}`), Connection: []byte(`{}`), IsEnabled: true}}}
	client := &probeMCPProvider{tools: []einotool.BaseTool{mcpInfoTool{"fetch"}}}
	s := NewMCPService(r, p, client)
	enable := true
	if _, err := s.UpdateTool(ctx, "u", "c", request.UpdateMCPToolRequest{Name: "fetch", IsEnabled: &enable}); err == nil || client.calls != 0 {
		t.Fatal("管理员停用后仍能启用工具或连接远端")
	}
	stop := false
	if _, err := s.UpdateTool(ctx, "u", "c", request.UpdateMCPToolRequest{Name: "fetch", IsEnabled: &stop}); err != nil {
		t.Fatal("系统预置失效后无法停用工具", err)
	}
}

func TestMCPToolDisplayAndSwitchPersistsWithoutLosingCredentials(t *testing.T) {
	ctx := context.Background()
	repo := &mcpMemoryRepo{rows: map[string]entity.MCPConnection{"c": {ID: "c", UserID: "u", DisplayName: "搜索", IsEnabled: true, Config: []byte(`{"api_key":"keep-secret","disabled_tools":["fetch"]}`), Connection: []byte(`{"transport":"http","url":"https://example.com/mcp"}`)}}}
	client := &probeMCPProvider{tools: []einotool.BaseTool{mcpInfoTool{"search"}, mcpInfoTool{"fetch"}}}
	s := NewMCPService(repo, nil, client)
	list, err := s.Tools(ctx, "u", "c")
	if err != nil || len(list.Tools) != 2 || !list.Tools[0].IsEnabled || list.Tools[1].IsEnabled || list.Tools[0].Description == "" {
		t.Fatal("工具展示或状态错误", err)
	}
	stop := false
	if _, err = s.UpdateTool(ctx, "other", "c", request.UpdateMCPToolRequest{Name: "search", IsEnabled: &stop}); err == nil {
		t.Fatal("允许跨用户工具启停")
	}
	client.err = errors.New("包含secret的远端错误")
	if _, err = s.Tools(ctx, "u", "c"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("发现失败未脱敏")
	}
	before := client.calls
	if _, err = s.UpdateTool(ctx, "u", "c", request.UpdateMCPToolRequest{Name: "search", IsEnabled: &stop}); err != nil || client.calls != before {
		t.Fatal("断网时不能停用", err)
	}
	client.err = nil
	enable := true
	if _, err = s.UpdateTool(ctx, "u", "c", request.UpdateMCPToolRequest{Name: "missing", IsEnabled: &enable}); err == nil {
		t.Fatal("启用了不存在的工具")
	}
	if _, err = s.UpdateTool(ctx, "u", "c", request.UpdateMCPToolRequest{Name: "fetch", IsEnabled: &enable}); err != nil {
		t.Fatal(err)
	}
	// 凭据表单的保存不能清空工具偏好或通过伪造disabled_tools启用它。
	config := json.RawMessage(`{"api_key":"new-secret","disabled_tools":[]}`)
	if _, err = s.Update(ctx, "u", "c", request.UpdateMCPConnectionRequest{Config: &config}); err != nil {
		t.Fatal(err)
	}
	detail, _ := s.Get(ctx, "u", "c")
	if !strings.Contains(string(detail.Config), "new-secret") || !strings.Contains(string(detail.Config), `"search"`) {
		t.Fatal("编辑凭据丢失工具偏好")
	}
	if _, err = s.Update(ctx, "u", "c", request.UpdateMCPConnectionRequest{IsEnabled: &stop}); err != nil {
		t.Fatal(err)
	}
	list, err = s.Tools(ctx, "u", "c")
	if err != nil || list.ConnectionEnabled || list.Tools[0].IsEnabled || !list.Tools[1].IsEnabled || list.Tools[1].EffectiveEnabled {
		t.Fatal("连接停用未覆盖工具或丢失偏好", err)
	}
	if _, err = s.Update(ctx, "u", "c", request.UpdateMCPConnectionRequest{IsEnabled: &enable}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Tools(ctx, "u", "c")
	if !list.Tools[1].EffectiveEnabled || list.Tools[0].EffectiveEnabled {
		t.Fatal("重启连接未保留工具偏好")
	}
	for _, req := range []request.UpdateMCPToolRequest{{Name: "search"}, {Name: "\n", IsEnabled: &stop}} {
		if _, err = s.UpdateTool(ctx, "u", "c", req); err == nil {
			t.Fatal("非法开关参数被接受")
		}
	}
}
