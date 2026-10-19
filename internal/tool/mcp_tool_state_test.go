package tool

import (
	"strings"
	"testing"
)

func TestMCPInvalidToolStateCannotGrantTools(t *testing.T) {
	for _, raw := range []string{`{"disabled_tools":"fetch"}`, `{"disabled_tools":[12]}`, `{"disabled_tools":[""]}`, `{"disabled_tools":["line\nbreak"]}`, `{"disabled_tools":[` + strings.Repeat(`"fetch",`, 128) + `"last"]}`} {
		c := newMCPConfig(raw)
		if _, _, err := ResolveMCPConnection(&c); err == nil {
			t.Fatal("非法工具权限配置被当作全部启用")
		}
	}
	for _, raw := range []string{`{}`, `{"disabled_tools":null}`, `{"disabled_tools":[]}`} {
		if disabled, err := DisabledMCPTools([]byte(raw)); err != nil || len(disabled) != 0 {
			t.Fatal("有效空偏好被拒绝", err)
		}
	}
}
