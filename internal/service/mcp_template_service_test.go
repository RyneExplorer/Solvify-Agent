package service

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	"solvify-agent/internal/tool/mcpclient"
	apperrors "solvify-agent/pkg/errors"
	"strings"
	"testing"
	"time"
)

type templateMemoryRepo struct {
	repository.MCPTemplateRepository
	saved    entity.MCPTemplate
	writes   int
	err      error
	manifest []byte
}

func (r *templateMemoryRepo) GetByID(context.Context, string) (*entity.MCPTemplate, error) {
	t := r.saved
	return &t, r.err
}
func (r *templateMemoryRepo) Create(_ context.Context, t *entity.MCPTemplate) error {
	r.saved = *t
	r.writes++
	return r.err
}
func (r *templateMemoryRepo) Update(_ context.Context, t *entity.MCPTemplate) error {
	r.saved = *t
	r.writes++
	return r.err
}
func (r *templateMemoryRepo) Delete(context.Context, string) error { r.writes++; return r.err }
func (r *templateMemoryRepo) List(context.Context) ([]entity.MCPTemplate, error) {
	return []entity.MCPTemplate{r.saved}, r.err
}

func (r *templateMemoryRepo) UpdateManifest(_ context.Context, _ string, _, manifest []byte) error {
	r.manifest = append([]byte(nil), manifest...)
	return r.err
}

func TestMCPTemplateProbeDiscoversWithoutExecutingAndRequiresCredentials(t *testing.T) {
	repo := &templateMemoryRepo{saved: entity.MCPTemplate{Connection: []byte(`{"transport":"http","url":"https://example.invalid/mcp"}`)}}
	client := &probeMCPProvider{}
	s := NewMCPTemplateService(repo, client)
	out, err := s.Probe(context.Background(), request.ProbeMCPTemplateRequest{ID: "template"})
	if err != nil || !out.Success || client.calls != 1 || string(repo.manifest) != "[]" {
		t.Fatal("发现或空工具清单保存不正确", err)
	}
	repo.saved.Connection = []byte(`{"transport":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"Bearer {{api_key}}"}}`)
	if _, err = s.Probe(context.Background(), request.ProbeMCPTemplateRequest{ID: "template"}); err == nil || client.calls != 1 {
		t.Fatal("没有用户凭据就连接模板")
	}
}

func TestMCPTemplateTimeoutsAndSystemProtection(t *testing.T) {
	ctx := context.Background()
	for _, system := range []bool{false, true} {
		repo := &templateMemoryRepo{saved: entity.MCPTemplate{ID: "template", Key: "search", ToolPrefix: "legacy_search", Name: "搜索", IsSystem: system, IsEnabled: true, Connection: datatypes.JSON(`{"transport":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"secret"},"extension":{"keep":true},"timeout":45}`)}}
		s := NewMCPTemplateService(repo, mcpclient.NewMCPClient(nil))
		for _, raw := range []string{`{"connect_timeout":90,"call_timeout":120}`, `{"connect_timeout":30,"call_timeout":60}`, `{}`} {
			patch := json.RawMessage(raw)
			if !system {
				patch = json.RawMessage(`{"transport":"http","url":"https://example.invalid/mcp",` + strings.TrimPrefix(raw, "{"))
				if raw == `{}` {
					patch = json.RawMessage(`{"transport":"http","url":"https://example.invalid/mcp"}`)
				}
			}
			if _, err := s.Update(ctx, "template", request.UpdateMCPTemplateRequest{Connection: &patch}); err != nil {
				t.Fatal(err)
			}
			var cfg tool.MCPConfig
			_ = json.Unmarshal(repo.saved.Connection, &cfg)
			if system && (cfg.Headers["Authorization"] != "secret" || !cfg.AdminTimeouts || !strings.Contains(string(repo.saved.Connection), `"keep":true`)) {
				t.Fatal("部署预置字段丢失")
			}
			if repo.saved.ToolPrefix != "legacy_search" {
				t.Fatal("工具名发生变化")
			}
		}
		var cfg tool.MCPConfig
		_ = json.Unmarshal(repo.saved.Connection, &cfg)
		if cfg.ConnectionTimeout() != 30*time.Second || cfg.ToolCallTimeout() != 300*time.Second {
			t.Fatal("清空未恢复默认")
		}
		for _, raw := range []string{`{"connect_timeout":0}`, `{"call_timeout":86401}`, `{"call_timeout":1.5}`, `{"connect_timeout":"30"}`} {
			patch := json.RawMessage(raw)
			before := repo.writes
			if _, err := s.Update(ctx, "template", request.UpdateMCPTemplateRequest{Connection: &patch}); err == nil || before != repo.writes {
				t.Fatal("非法配置被保存")
			}
		}
		if system {
			for _, raw := range []string{`{"url":"https://attacker.invalid"}`, `{"headers":{}}`, `{"admin_timeouts":false}`} {
				patch := json.RawMessage(raw)
				before := repo.writes
				if _, err := s.Update(ctx, "template", request.UpdateMCPTemplateRequest{Connection: &patch}); err == nil || before != repo.writes {
					t.Fatal("部署预置保护失效")
				}
			}
			name := "changed"
			if _, err := s.Update(ctx, "template", request.UpdateMCPTemplateRequest{Name: &name}); err == nil {
				t.Fatal("部署预置名称可改")
			}
			before := repo.writes
			if err := s.Delete(ctx, "template"); err == nil || before != repo.writes {
				t.Fatal("部署预置可删除")
			}
		}
		rows, err := s.List(ctx)
		data, _ := json.Marshal(rows)
		if err != nil || strings.Contains(string(data), "secret") {
			t.Fatal("列表泄露连接凭据")
		}
	}
}
func TestMCPTemplateCreateValidationAndNotFound(t *testing.T) {
	ctx := context.Background()
	repo := &templateMemoryRepo{}
	s := NewMCPTemplateService(repo, mcpclient.NewMCPClient(nil))
	req := request.CreateMCPTemplateRequest{Key: "search", Name: "搜索", Connection: json.RawMessage(`{"transport":"http","url":"https://example.invalid/mcp","connect_timeout":90,"call_timeout":120}`)}
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if repo.saved.ToolPrefix != "mcp_search" {
		t.Fatal("新预置工具名不稳定")
	}
	for _, raw := range []string{`null`, `[]`, `{"transport":"http","url":"https://{{host}}/mcp"}`, `{"transport":"stdio","command":"test"}`, `{"transport":"http","url":"https://example.invalid","call_timeout":0}`} {
		req.Connection = json.RawMessage(raw)
		before := repo.writes
		if _, err := s.Create(ctx, req); err == nil || before != repo.writes {
			t.Fatal("非法连接被创建", raw)
		}
	}
	for _, tc := range []struct {
		err  error
		code int
	}{{gorm.ErrRecordNotFound, apperrors.CodeMCPTemplateNotFound}, {errors.New("database unavailable"), apperrors.CodeInternalError}} {
		repo.err = tc.err
		_, err := s.GetByID(ctx, "template")
		var biz *apperrors.BizError
		if !errors.As(err, &biz) || biz.Code != tc.code || !errors.Is(err, tc.err) {
			t.Fatal("查询错误分类或原因丢失", err)
		}
	}
}
