package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"

	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/tokenutil"
)

type artifactReadPage struct {
	Content      string `json:"content"`
	Offset       int    `json:"offset"`
	NextOffset   int    `json:"next_offset"`
	TotalRunes   int    `json:"total_runes"`
	MaxReadRunes int    `json:"max_read_runes"`
	Complete     bool   `json:"complete"`
}

func artifactReadFixture(t *testing.T, policy config.RuntimeContextPolicy, content string) (artifactReadTool, artifactReference, string) {
	t.Helper()
	e := deepTestEngine()
	e.cfg.RuntimeContext = policy
	repo := &memoryArtifactRepo{}
	e.WithArtifactRepo(repo)
	req := Request{UserID: "u", SessionID: "s", RunID: "run", AgentName: "knowledge_research", TaskID: "t", ModelName: "gpt-4o"}
	endpoint := e.artifactMiddleware(req)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: content}, nil
	})
	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "search", CallID: "call"})
	if err != nil {
		t.Fatal(err)
	}
	var ref artifactReference
	if err = json.Unmarshal([]byte(out.Result), &ref); err != nil || ref.ArtifactID == "" {
		t.Fatalf("缺少产物引用: %v", err)
	}
	return artifactReadTool{repo, artifactScope(req), policy, req.ModelName}, ref, out.Result
}

func TestContextArtifactReadClampsLimit(t *testing.T) {
	content := strings.Repeat("证据📄\"\\\n", 1600)
	for _, maxRunes := range []int{4000, 37} {
		t.Run(fmt.Sprint(maxRunes), func(t *testing.T) {
			policy := config.DefaultRuntimeContextPolicy()
			policy.ReadRunes = maxRunes
			reader, ref, _ := artifactReadFixture(t, policy, content)
			for _, limit := range []int{-1, 0, 1, maxRunes, maxRunes + 1, 6000, math.MaxInt} {
				name := fmt.Sprint(limit)
				if limit == -1 {
					name = "omitted"
				}
				t.Run(name, func(t *testing.T) {
					args := map[string]any{"artifact_id": ref.ArtifactID, "offset": 1000}
					if limit >= 0 {
						args["limit"] = limit
					}
					data, _ := json.Marshal(args)
					raw, err := reader.InvokableRun(context.Background(), string(data))
					if err != nil {
						t.Fatalf("有界读取不应失败: %v", err)
					}
					var page artifactReadPage
					if err = json.Unmarshal([]byte(raw), &page); err != nil {
						t.Fatal(err)
					}
					bound := maxRunes
					if limit > 0 {
						bound = min(bound, limit)
					}
					count := len([]rune(page.Content))
					if count == 0 || count > bound || page.Offset != 1000 || page.NextOffset != 1000+count || page.TotalRunes != len([]rune(content)) || page.Complete {
						t.Fatalf("读取范围不符合契约: %+v", page)
					}
					if page.Content != string([]rune(content)[1000:page.NextOffset]) {
						t.Fatal("字符偏移读取不匹配原文")
					}
					if cost := tokenutil.CountTokens(raw, reader.model); cost > policy.ReadTokens {
						t.Fatalf("序列化响应超出token预算: %d > %d", cost, policy.ReadTokens)
					}
				})
			}
		})
	}
}

func TestContextArtifactReadPublishesRuntimeLimit(t *testing.T) {
	policy := config.DefaultRuntimeContextPolicy()
	policy.ReadRunes = 1234
	reader, _, raw := artifactReadFixture(t, policy, strings.Repeat("原文证据", 1500))
	var reference struct {
		MaxReadRunes int    `json:"max_read_runes"`
		Read         string `json:"read"`
	}
	if err := json.Unmarshal([]byte(raw), &reference); err != nil {
		t.Fatal(err)
	}
	if reference.MaxReadRunes != policy.ReadRunes || !strings.Contains(reference.Read, "1234") || !strings.Contains(reference.Read, "next_offset") {
		t.Fatalf("引用未公开运行上限和分页方式: %s", raw)
	}
	info, err := reader.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	params, err := info.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	limit, ok := params.Properties.Get("limit")
	if !ok || !strings.Contains(limit.Description, "1234") || !strings.Contains(info.Desc, "next_offset") {
		t.Fatalf("模型工具定义未公开运行上限: %+v", info)
	}
}

func TestContextArtifactOversizedReadPagination(t *testing.T) {
	policy := config.DefaultRuntimeContextPolicy()
	policy.ArtifactThreshold = 1
	policy.ReadTokens = 260
	content := strings.Repeat("证据📄\"\\\n", 240)
	reader, ref, _ := artifactReadFixture(t, policy, content)
	var rebuilt strings.Builder
	var pages int
	for offset := 0; ; {
		raw, err := reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q,"offset":%d,"limit":6000}`, ref.ArtifactID, offset))
		if err != nil {
			t.Fatalf("offset=%d: %v", offset, err)
		}
		var page artifactReadPage
		if err = json.Unmarshal([]byte(raw), &page); err != nil {
			t.Fatal(err)
		}
		count := len([]rune(page.Content))
		if page.Offset != offset || page.NextOffset != offset+count || page.NextOffset > len([]rune(content)) || page.MaxReadRunes != policy.ReadRunes {
			t.Fatalf("分页元数据无效: %+v", page)
		}
		if cost := tokenutil.CountTokens(raw, reader.model); cost > policy.ReadTokens {
			t.Fatalf("序列化响应超出token预算: %d > %d", cost, policy.ReadTokens)
		}
		rebuilt.WriteString(page.Content)
		pages++
		if page.Complete {
			break
		}
		if page.NextOffset <= offset || pages > len([]rune(content)) {
			t.Fatal("分页没有进展")
		}
		offset = page.NextOffset
	}
	if pages < 2 || rebuilt.String() != content {
		t.Fatal("token裁剪后未完整还原原文")
	}
	raw, err := reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q,"offset":%d,"limit":6000}`, ref.ArtifactID, len([]rune(content))))
	var end artifactReadPage
	if err != nil || json.Unmarshal([]byte(raw), &end) != nil || !end.Complete || end.Content != "" || end.NextOffset != len([]rune(content)) {
		t.Fatalf("末尾读取错误: %s, %v", raw, err)
	}
}

func TestContextArtifactOversizedReadPreservesFailures(t *testing.T) {
	reader, ref, _ := artifactReadFixture(t, config.DefaultRuntimeContextPolicy(), strings.Repeat("原文证据", 1500))
	args := fmt.Sprintf(`{"artifact_id":%q,"offset":1000,"limit":6000}`, ref.ArtifactID)
	other := reader
	other.scope.UserID = "other"
	if _, err := other.InvokableRun(context.Background(), args); !errors.Is(err, repository.ErrArtifactUnavailable) {
		t.Fatalf("超大limit绕过归属检查: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.InvokableRun(ctx, args); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消被吞掉: %v", err)
	}
	other = reader
	other.policy.ReadTokens = 1
	if _, err := other.InvokableRun(context.Background(), args); err == nil {
		t.Fatal("预算不足返回了伪成功")
	}
}
