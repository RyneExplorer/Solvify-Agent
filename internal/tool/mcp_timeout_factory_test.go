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

func TestMCPTimeoutFactoryReadsSavedValues(t *testing.T) {
	config := newMCPConfig(`{"timeout":99999}`)
	store := &fakeConfigStore{}
	client := &fakeMCPClient{tools: []einotool.BaseTool{deadlineProbeTool{}}}
	factory := NewFactory(client, store)
	for _, seconds := range []int{30, 60, 0} {
		config.Template.Connection = datatypes.JSON(fmt.Sprintf(`{"transport":"http","url":"https://example.invalid","timeout":%d}`, seconds))
		store.configs = []entity.MCPConnection{config}
		tools := factory.CreateAgentTools(context.Background(), "u1")
		if len(tools) != 1 {
			t.Fatal("MCP工具未构建")
		}
		got, err := tools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{"timeout":99999}`)
		want := seconds
		if want == 0 {
			want = 300
		}
		if err != nil || got != strconv.Itoa(want) {
			t.Fatalf("存储%d，实际%s: %v", seconds, got, err)
		}
	}
	config.Template.Connection = datatypes.JSON(`{"transport":"http","url":"https://example.invalid","timeout":45,"connect_timeout":90,"call_timeout":120}`)
	store.configs = []entity.MCPConnection{config}
	tools := factory.CreateAgentTools(context.Background(), "u1")
	if len(tools) != 1 {
		t.Fatal("MCP工具构建失败")
	}
	got, err := tools[0].(einotool.InvokableTool).InvokableRun(context.Background(), `{}`)
	if err != nil || got != "120" {
		t.Fatal("实际调用未优先使用独立调用时限", got, err)
	}
}
