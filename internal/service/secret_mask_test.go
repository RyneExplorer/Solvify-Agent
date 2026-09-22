package service

import (
	"strings"
	"testing"

	"solvify-agent/internal/model/entity"
)

// ─── P0-2 回归测试：响应里的 API Key 必须脱敏 ────────────────────────────────
//
// 背景：此前 GET /api/v1/models 只挂登录鉴权，且 toModelInfo 原样透传 m.APIKey，
// 任意普通登录用户即可拿到平台全部上游密钥（OpenAI / DeepSeek…）并盗用配额。
// 这里把「脱敏」这一层钉死：即使将来有人把路由权限重新放开，响应体也不会泄露密钥。

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空值原样返回，便于前端区分未配置", "", ""},
		{"短密钥整体隐藏", "1234567", "****"},
		{"常见 sk- 密钥保留头3尾4", "sk-abcdefghijklmnop", "sk-****mnop"},
		{"恰好8位保留头3尾4", "12345678", "123****5678"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maskAPIKey(c.in); got != c.want {
				t.Fatalf("maskAPIKey(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// 脱敏结果不能包含原始密钥的可还原片段（尤其不能把整串吐回来）。
func TestMaskAPIKey_NeverLeaksWholeKey(t *testing.T) {
	const raw = "sk-live-9f8a7b6c5d4e3f2a1b0c"

	got := maskAPIKey(raw)
	if strings.Contains(got, raw) {
		t.Fatalf("脱敏结果仍包含完整密钥: %q", got)
	}
	// 中段必须被顶掉：原始第 4~len-4 位不该以任何形式出现。
	if strings.Contains(got, raw[3:len(raw)-4]) {
		t.Fatalf("脱敏结果泄露了密钥中段: %q", got)
	}
	// 仍能让人分辨「是哪一把」——头尾保留。
	if !strings.HasPrefix(got, raw[:3]) || !strings.HasSuffix(got, raw[len(raw)-4:]) {
		t.Fatalf("脱敏结果丢失了辨识用的头/尾: %q", got)
	}
}

// toModelInfo 是 List / GetByID / Create 三个响应共用的构造器，
// 只要它脱敏，所有回显路径就都堵住了。
func TestToModelInfo_MasksAPIKey(t *testing.T) {
	m := entity.Model{
		ID:               "m-1",
		Name:             "gpt-5.5",
		Provider:         "openai",
		ModelID:          "gpt-5.5",
		BaseURL:          "https://example.com/v1",
		APIKey:           "sk-live-9f8a7b6c5d4e3f2a1b0c",
		IsEnabled:        true,
		MaxContextLength: 8192,
	}

	got := toModelInfo(m)

	// 期望值独立硬编码，不复用 maskAPIKey —— 否则测试只是实现的自证，
	// 实现改错了测试也跟着错，等于没测。
	if got.APIKey != "sk-****1b0c" {
		t.Fatalf("toModelInfo 的 APIKey = %q，期望 %q", got.APIKey, "sk-****1b0c")
	}
	if strings.Contains(got.APIKey, m.APIKey) {
		t.Fatalf("toModelInfo 原样透传了 APIKey: %q", got.APIKey)
	}
	// 其余字段必须保持原样，别顺手改坏。
	if got.ID != m.ID || got.Name != m.Name || got.ModelID != m.ModelID || got.BaseURL != m.BaseURL {
		t.Fatalf("脱敏误伤了其他字段: %+v", got)
	}
}
