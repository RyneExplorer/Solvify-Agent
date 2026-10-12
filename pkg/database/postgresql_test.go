package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// 这两个函数守的是同一件事：**启动期建索引失败时，报出来的话必须是可确证的**。
//
// 历史缺陷：EnsurePGVectorIndex 对所有失败一律附
// 「低流量环境可能需要先执行 VACUUM ANALYZE document_chunks」，
// 而实际最常发生的 SQLSTATE 22023 是「embedding 列没有维度」——
// VACUUM ANALYZE 对它完全无效，这条提示会把排查方向带反。
// 因此规则改成：能确证成因才给建议，不能确证就一个字都不猜。

// TestVectorColumnHasNoDimension 判据是「有没有维度修饰符」，不是猜列名或比对常量。
func TestVectorColumnHasNoDimension(t *testing.T) {
	cases := []struct {
		name      string
		formatted string
		want      bool
	}{
		{"无维度 vector（本次线上真实形态）", "vector", true},
		{"带维度 vector(1024)", "vector(1024)", false},
		{"带空格的维度写法", "vector( 1024 )", false},
		{"非 vector 类型（有修饰符）", "character varying(128)", false},
		{"非 vector 类型（无修饰符）", "text", false},
		{"查不到类型（空串）", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vectorColumnHasNoDimension(c.formatted); got != c.want {
				t.Fatalf("vectorColumnHasNoDimension(%q) = %v，期望 %v", c.formatted, got, c.want)
			}
		})
	}
}

// TestPGIndexFailureHint 已确证的类别必须给出「指向正确解法」的建议。
func TestPGIndexFailureHint(t *testing.T) {
	cases := []struct {
		name         string
		code         string
		message      string
		wantContains []string
	}{
		{
			name:         "22023 列没有维度",
			code:         sqlstateInvalidParameterValue,
			message:      "column does not have dimensions",
			wantContains: []string{"无维度", "ALTER COLUMN", "vector("},
		},
		{
			name:         "42501 权限不足",
			code:         sqlstateInsufficientPrivilege,
			message:      "permission denied for table document_chunks",
			wantContains: []string{"权限"},
		},
		{
			name:         "42P01 表不存在",
			code:         sqlstateUndefinedTable,
			message:      `relation "document_chunks" does not exist`,
			wantContains: []string{"表不存在", "schema"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hint := pgIndexFailureHint(&pgconn.PgError{Code: c.code, Message: c.message})
			if hint == "" {
				t.Fatalf("SQLSTATE %s 应给出建议，实际为空", c.code)
			}
			for _, want := range c.wantContains {
				if !strings.Contains(hint, want) {
					t.Fatalf("SQLSTATE %s 的建议里应含 %q，实际: %s", c.code, want, hint)
				}
			}
		})
	}
}

// TestPGIndexFailureHintRejectsSameCodeOtherCause 防「只看 SQLSTATE 码就归因」。
//
// 22023 是「无效参数值」大类：pgvector 除了「列没有维度」，还会用它报
// 「lists 必须 1..32768」「维度超过索引方法上限」等**完全不同的**参数错误。
// 只看码就输出「列没有维度」的建议，等于重犯本函数要修的那个毛病。
// 判据必须是「码 + 消息」两个都对上，缺一不可。
func TestPGIndexFailureHintRejectsSameCodeOtherCause(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		message string
	}{
		{"22023 但成因是 lists 越界", sqlstateInvalidParameterValue, "lists must be between 1 and 32768"},
		{"22023 但成因是维度超上限", sqlstateInvalidParameterValue, "column cannot have more than 2000 dimensions for ivfflat index"},
		{"22023 但消息为空", sqlstateInvalidParameterValue, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if hint := pgIndexFailureHint(&pgconn.PgError{Code: c.code, Message: c.message}); hint != "" {
				t.Fatalf("成因未确证时不应给建议，实际: %s", hint)
			}
		})
	}
}

// TestPGIndexFailureHintNeverFabricatesCause 核心回归：
//   - 不是 *pgconn.PgError 的错误 → 不给建议
//   - 已识别但未归类的 SQLSTATE → 不给建议
//   - 22023 的建议里不得出现「VACUUM ANALYZE 可能是解法」这类未经确证的归因
func TestPGIndexFailureHintNeverFabricatesCause(t *testing.T) {
	t.Run("非 PgError 不给建议", func(t *testing.T) {
		if hint := pgIndexFailureHint(errors.New("connection reset by peer")); hint != "" {
			t.Fatalf("未识别错误类型不应给建议，实际: %s", hint)
		}
	})

	t.Run("未归类 SQLSTATE 不给建议", func(t *testing.T) {
		// XX000 = internal_error，成因不确定 → 必须只说原始错误
		if hint := pgIndexFailureHint(&pgconn.PgError{Code: "XX000"}); hint != "" {
			t.Fatalf("未归类 SQLSTATE 不应给建议，实际: %s", hint)
		}
	})

	t.Run("文本里含 PgError 文本但不带 %w", func(t *testing.T) {
		// 即使错误文本里拼了 PgError 的 Error() 输出，没有 %w 链也取不到类型。
		// 这条专门防「按错误文本做字符串匹配」的退化实现被误当成已分类。
		plain := errors.New("wrap: " + (&pgconn.PgError{
			Code:    sqlstateInvalidParameterValue,
			Message: "column does not have dimensions",
		}).Error())
		if hint := pgIndexFailureHint(plain); hint != "" {
			t.Fatalf("非 PgError 不应给建议，实际: %s", hint)
		}
	})

	t.Run("%w 包装的 PgError 仍给建议", func(t *testing.T) {
		// 上层用 %w 包一层后仍要能分类，否则「包装一下就丢诊断」。
		wrapped := fmt.Errorf("写库失败: %w", &pgconn.PgError{
			Code:    sqlstateInvalidParameterValue,
			Message: "column does not have dimensions",
		})
		if hint := pgIndexFailureHint(wrapped); hint == "" {
			t.Fatal("errors.As 能取到的 PgError 必须给出建议")
		}
	})

	t.Run("22023 不得把 VACUUM ANALYZE 说成解法", func(t *testing.T) {
		hint := pgIndexFailureHint(&pgconn.PgError{
			Code:    sqlstateInvalidParameterValue,
			Message: "column does not have dimensions",
		})
		if hint == "" {
			t.Fatal("这条断言的前提是 22023 已给出建议；为空说明断言会变成空洞")
		}
		for _, forbidden := range []string{"可能需要先执行 VACUUM", "先执行 VACUUM ANALYZE", "建议 VACUUM"} {
			if strings.Contains(hint, forbidden) {
				t.Fatalf("22023 的建议里出现了未经确证的归因 %q，实际: %s", forbidden, hint)
			}
		}
	})
}
