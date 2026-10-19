package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
)

type mcpMemoryRepo struct {
	repository.MCPRepository
	rows   map[string]entity.MCPConnection
	writes int
}

func (r *mcpMemoryRepo) Create(_ context.Context, c *entity.MCPConnection) error {
	r.writes++
	c.ID = "new-id"
	r.rows[c.ID] = *c
	return nil
}
func (r *mcpMemoryRepo) Update(_ context.Context, c *entity.MCPConnection) error {
	r.writes++
	r.rows[c.ID] = *c
	return nil
}
func (r *mcpMemoryRepo) SetToolEnabled(_ context.Context, userID, id, name string, enabled bool) error {
	c, ok := r.rows[id]
	if !ok || c.UserID != userID {
		return gorm.ErrRecordNotFound
	}
	var config map[string]interface{}
	_ = json.Unmarshal(c.Config, &config)
	disabled, err := tool.DisabledMCPTools(c.Config)
	if err != nil {
		return err
	}
	delete(disabled, name)
	if !enabled {
		disabled[name] = true
	}
	names := []string{}
	for n := range disabled {
		names = append(names, n)
	}
	config["disabled_tools"] = names
	c.Config, _ = json.Marshal(config)
	r.rows[id] = c
	r.writes++
	return nil
}
func (r *mcpMemoryRepo) Get(_ context.Context, userID, id string) (*entity.MCPConnection, error) {
	c, ok := r.rows[id]
	if !ok || c.UserID != userID {
		return nil, gorm.ErrRecordNotFound
	}
	return &c, nil
}
func (r *mcpMemoryRepo) List(_ context.Context, userID string) ([]entity.MCPConnection, error) {
	out := []entity.MCPConnection{}
	for _, c := range r.rows {
		if c.UserID == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

type mcpTemplateRepo struct {
	repository.MCPTemplateRepository
	p entity.MCPTemplate
}

func (r *mcpTemplateRepo) GetByID(context.Context, string) (*entity.MCPTemplate, error) {
	p := r.p
	return &p, nil
}

func (r *mcpTemplateRepo) List(context.Context) ([]entity.MCPTemplate, error) {
	return []entity.MCPTemplate{r.p}, nil
}

func TestMCPTemplateListFiltersDisabledAndHidesConnectionSecrets(t *testing.T) {
	r := &mcpTemplateRepo{p: entity.MCPTemplate{ID: "template", IsEnabled: true, Connection: datatypes.JSON(`{"headers":{"Authorization":"secret"}}`), ConfigSchema: datatypes.JSON(`{"type":"object","properties":{}}`)}}
	s := NewMCPService(nil, r, nil)
	rows, err := s.Templates(context.Background())
	data, _ := json.Marshal(rows)
	if err != nil || len(rows) != 1 || strings.Contains(string(data), "secret") {
		t.Fatalf("预置列表不正确: %s %v", data, err)
	}
	r.p.IsEnabled = false
	if rows, err = s.Templates(context.Background()); err != nil || len(rows) != 0 {
		t.Fatal("停用预置仍可绑定")
	}
}

type probeMCPProvider struct {
	cfg   *tool.MCPConfig
	calls int
	tools []einotool.BaseTool
	err   error
}

func (p *probeMCPProvider) Validate(*tool.MCPConfig) error { return nil }

func (p *probeMCPProvider) GetTools(_ context.Context, c *tool.MCPConfig) ([]einotool.BaseTool, error) {
	p.cfg = c
	p.calls++
	return p.tools, p.err
}

type mcpInfoTool struct{ name string }

func (t mcpInfoTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "工具用途说明"}, nil
}
func TestMCPServiceTemplateCredentialsAndOwnership(t *testing.T) {
	ctx := context.Background()
	id := "template"
	repo := &mcpMemoryRepo{rows: map[string]entity.MCPConnection{}}
	templates := &mcpTemplateRepo{p: entity.MCPTemplate{ID: id, Key: "search", IsEnabled: true, ToolPrefix: "mcp_filesystem", ConfigSchema: datatypes.JSON(`{"type":"object","properties":{"api_key":{"type":"string","secret":true}},"required":["api_key"]}`), Connection: datatypes.JSON(`{"transport":"http","url":"https://search.example/mcp","headers":{"Authorization":"Bearer {{api_key}}"}}`)}}
	p := &probeMCPProvider{}
	s := NewMCPService(repo, templates, p)
	req := request.CreateMCPConnectionRequest{TemplateID: &id, DisplayName: "搜索", Config: json.RawMessage(`{}`)}
	if _, err := s.Create(ctx, "alice", req); err == nil || repo.writes != 0 {
		t.Fatal("空凭据被保存")
	}
	req.Config = json.RawMessage(`{"api_key":"alice-secret"}`)
	req.Connection = json.RawMessage(`{ }`)
	created, err := s.Create(ctx, "alice", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Config) > 0 || len(created.Connection) > 0 {
		t.Fatal("创建列表响应泄露凭据")
	}
	if _, err = s.Get(ctx, "bob", created.ID); err == nil {
		t.Fatal("跨用户读凭据")
	}
	detail, err := s.Get(ctx, "alice", created.ID)
	if err != nil || !json.Valid(detail.Config) {
		t.Fatal("本人无法编辑", err)
	}
	if _, err = s.Probe(ctx, "bob", request.ProbeMCPConnectionRequest{ID: created.ID}); err == nil || p.calls != 0 {
		t.Fatal("跨用户探测")
	}
	if _, err = s.Probe(ctx, "alice", request.ProbeMCPConnectionRequest{ID: created.ID}); err != nil {
		t.Fatal(err)
	}
	if p.cfg.Headers["Authorization"] != "Bearer alice-secret" || p.cfg.ScopeKey != "alice:new-id" {
		t.Fatal("用户Key未绑定或连接未隔离")
	}
	var original tool.MCPConfig
	_ = json.Unmarshal(templates.p.Connection, &original)
	if original.Headers["Authorization"] != "Bearer {{api_key}}" {
		t.Fatal("共享模板被改写")
	}
	override := json.RawMessage(`{"url":"https://attacker.example/mcp"}`)
	if _, err = s.Update(ctx, "alice", created.ID, request.UpdateMCPConnectionRequest{Connection: &override}); err == nil {
		t.Fatal("用户能覆盖模板端点")
	}
	templates.p.IsEnabled = false
	enabled := true
	if _, err = s.Update(ctx, "alice", created.ID, request.UpdateMCPConnectionRequest{IsEnabled: &enabled}); err == nil {
		t.Fatal("失效模板被启用")
	}
	enabled = false
	if _, err = s.Update(ctx, "alice", created.ID, request.UpdateMCPConnectionRequest{IsEnabled: &enabled}); err != nil {
		t.Fatal("无法停用失效模板", err)
	}
}
func TestMCPServicePersonalConfigValidation(t *testing.T) {
	repo := &mcpMemoryRepo{rows: map[string]entity.MCPConnection{}}
	s := NewMCPService(repo, nil, &probeMCPProvider{})
	for _, raw := range []string{`null`, `[]`, `{"transport":"stdio","command":"danger"}`, `{"transport":"http","url":"file:///tmp/test"}`, `{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"bad\r\nHeader: x"}}`, `{"transport":"http","url":"https://user:password@example.com/mcp"}`} {
		if _, err := s.Create(context.Background(), "alice", request.CreateMCPConnectionRequest{DisplayName: "个人连接", Connection: json.RawMessage(raw)}); err == nil {
			t.Fatalf("接受非法配置: %s", raw)
		}
	}
	if repo.writes != 0 {
		t.Fatal("非法配置写库")
	}
	c, err := s.Create(context.Background(), "alice", request.CreateMCPConnectionRequest{DisplayName: "个人连接", Connection: json.RawMessage(`{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer personal"}}`)})
	if err != nil || c.TemplateID != nil {
		t.Fatal(err)
	}
	rows, err := s.List(context.Background(), "alice")
	if err != nil || len(rows) != 1 || len(rows[0].Connection) > 0 {
		t.Fatal("列表暴露连接凭据")
	}
}
