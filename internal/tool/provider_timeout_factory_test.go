package tool

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"gorm.io/datatypes"
	"solvify-agent/internal/model/entity"
)

type deadlineProbeTool struct{}

func (deadlineProbeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "probe"}, nil
}
func (deadlineProbeTool) InvokableRun(ctx context.Context, _ string, _ ...einotool.Option) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", fmt.Errorf("无deadline")
	}
	return strconv.Itoa(int(time.Until(deadline).Round(time.Second) / time.Second)), nil
}

func TestProviderTimeoutFactoryReadsSavedValues(t *testing.T) {
	for _, kind := range []string{"mcp"} {
		t.Run(kind, func(t *testing.T) {
			config := newMCPConfig(`{"timeout":99999}`)
			config.Template.ProviderType = kind
			store := &fakeConfigStore{}
			var provider Provider
			if kind == "mcp" {
				provider = &fakeMultiProvider{tools: []einotool.BaseTool{deadlineProbeTool{}}}
			}
			f := NewFactory(&fakeRegistry{provider: provider}, store)
			for _, seconds := range []int{30, 60, 0} {
				raw := fmt.Sprintf(`{"method":"GET","url":"https://example.invalid","timeout":%d}`, seconds)
				if kind == "mcp" {
					raw = fmt.Sprintf(`{"mcp":{"transport":"http","url":"https://example.invalid","timeout":%d}}`, seconds)
				}
				config.Template.ProviderConfig = datatypes.JSON(raw)
				store.configs = []entity.MCPConnection{config}
				tools := f.CreateAgentTools(context.Background(), "u1")
				if len(tools) != 1 {
					t.Fatal("供应商工具未构建")
				}
				got, err := tools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"timeout":99999}`)
				want := seconds
				if kind == "mcp" && seconds == 0 {
					want = 300
				}
				if err != nil || got != strconv.Itoa(want) {
					t.Fatalf("%s存储%d，实际%s: %v", kind, seconds, got, err)
				}
			}
			if kind == "mcp" {
				config.Template.ProviderConfig = datatypes.JSON(`{"mcp":{"transport":"http","url":"https://example.invalid","timeout":45,"connect_timeout":90,"call_timeout":120}}`)
				store.configs = []entity.MCPConnection{config}
				tools := f.CreateAgentTools(context.Background(), "u1")
				if len(tools) != 1 {
					t.Fatal("新配置工具构建失败")
				}
				got, err := tools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{}`)
				if err != nil || got != "120" {
					t.Fatal("真实调用未优先使用独立调用时限", got, err)
				}
			}
		})
	}
}
