package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	"solvify-agent/internal/tool/providers"
)

type providerTimeoutRepo struct {
	repository.ToolProviderRepository
	saved  entity.ToolProvider
	writes int
}

func (r *providerTimeoutRepo) ExistsByKey(context.Context, string, string) (bool, error) {
	return false, nil
}
func (r *providerTimeoutRepo) Create(_ context.Context, p *entity.ToolProvider) error {
	r.saved = *p
	r.writes++
	return nil
}
func (r *providerTimeoutRepo) Update(_ context.Context, p *entity.ToolProvider) error {
	r.saved = *p
	r.writes++
	return nil
}
func (r *providerTimeoutRepo) GetByID(context.Context, string) (*entity.ToolProvider, error) {
	p := r.saved
	return &p, nil
}

type providerTimeoutTypeRepo struct{ repository.ToolTypeRepository }

func (providerTimeoutTypeRepo) GetByID(context.Context, string) (*entity.ToolType, error) {
	return &entity.ToolType{}, nil
}

func TestProviderTimeoutSavedThroughExistingService(t *testing.T) {
	for _, kind := range []string{"http", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			repo := &providerTimeoutRepo{}
			registry := tool.NewProviderRegistry()
			registry.Register("http", providers.NewHTTPProvider(30*time.Second))
			registry.Register("mcp", providers.NewMCPProvider(nil))
			svc := NewToolProviderService(repo, providerTimeoutTypeRepo{}, registry, nil, nil)
			makeConfig := func(seconds string) json.RawMessage {
				if kind == "mcp" {
					return json.RawMessage(`{"transport":"http","url":"https://example.invalid","timeout":` + seconds + `}`)
				}
				return json.RawMessage(`{"method":"GET","url":"https://example.invalid","timeout":` + seconds + `}`)
			}
			created, err := svc.Create(context.Background(), request.CreateToolProviderRequest{ToolTypeID: "type", ProviderKey: "key", Name: "test", ProviderType: kind, ProviderConfig: makeConfig("30")})
			if err != nil {
				t.Fatal(err)
			}
			readSeconds := func(raw []byte) int {
				var pc tool.ProviderConfig
				if err := json.Unmarshal(raw, &pc); err != nil {
					t.Fatal(err)
				}
				if kind == "mcp" {
					return pc.MCP.Timeout
				}
				return pc.Timeout
			}
			if readSeconds(created.ProviderConfig) != 30 {
				t.Fatal("创建未保存显式30秒")
			}
			raw := makeConfig("60")
			updated, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw})
			if err != nil || readSeconds(updated.ProviderConfig) != 60 {
				t.Fatalf("更新未保存: %+v %v", updated, err)
			}
			for _, value := range []string{"-1", "86401", "1.5", `"bad"`} {
				bad := makeConfig(value)
				writes := repo.writes
				if _, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &bad}); err == nil || writes != repo.writes {
					t.Fatalf("非法时限被写入: %s %v", value, err)
				}
				if _, err := svc.Create(context.Background(), request.CreateToolProviderRequest{ToolTypeID: "type", ProviderKey: "key", ProviderType: kind, ProviderConfig: bad}); err == nil || writes != repo.writes {
					t.Fatalf("非法时限被创建: %s %v", value, err)
				}
			}
			raw = json.RawMessage(`{"method":"GET","url":"https://example.invalid"}`)
			if kind == "mcp" {
				raw = json.RawMessage(`{"mcp":{"transport":"http","url":"https://example.invalid"}}`)
			}
			if _, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw}); err != nil {
				t.Fatal(err)
			}
			if readSeconds(repo.saved.ProviderConfig) != 0 {
				t.Fatal("清空时限没有恢复默认")
			}
		})
	}
}
