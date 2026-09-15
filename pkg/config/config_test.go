package config

import "testing"

// TestParseHeaderList 覆盖 OTEL_HEADERS 环境变量的解析，重点是含 '=' 的取值不被截断。
func TestParseHeaderList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "空字符串返回 nil",
			raw:  "",
			want: nil,
		},
		{
			name: "单个键值",
			raw:  "Authorization=Bearer token",
			want: map[string]string{"Authorization": "Bearer token"},
		},
		{
			name: "多个键值并去除两端空格",
			raw:  " Authorization = Bearer token , x-byteapm-appkey = abc ",
			want: map[string]string{"Authorization": "Bearer token", "x-byteapm-appkey": "abc"},
		},
		{
			name: "取值里的等号不被截断",
			raw:  "Authorization=Basic dXNlcjpwYXNz==",
			want: map[string]string{"Authorization": "Basic dXNlcjpwYXNz=="},
		},
		{
			name: "缺少等号的条目被忽略",
			raw:  "no-equals,Authorization=ok",
			want: map[string]string{"Authorization": "ok"},
		},
		{
			name: "等号开头视为非法",
			raw:  "=value",
			want: nil,
		},
		{
			name: "全部非法时返回 nil",
			raw:  ",,=",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHeaderList(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("条目数不符: got=%v want=%v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("键 %q 的取值不符: got=%q want=%q", k, got[k], v)
				}
			}
		})
	}
}
