package providers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"solvify-agent/internal/tool"
)

// 个人MCP每次拨号重新解析并固定连接IP，禁用代理和重定向，防止DNS重绑定和凭据转发。
func publicMCPAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, raw := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}

type mcpTransport struct {
	base   *http.Transport
	origin *url.URL
}

func (t *mcpTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != t.origin.Scheme || !strings.EqualFold(r.URL.Host, t.origin.Host) {
		return nil, fmt.Errorf("MCP请求目标不在授权范围")
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	response.Body = &mcpLimitedBody{ReadCloser: response.Body, remaining: 8 << 20}
	return response, nil
}

type mcpLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *mcpLimitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("MCP响应超过大小限制")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}
func mcpHTTPClient(cfg *tool.MCPProviderConfig) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	base := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConnsPerHost: 2}
	base.DialContext = dialer.DialContext
	if cfg.RestrictNetwork {
		base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("MCP目标地址无效")
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("MCP地址解析失败")
			}
			for _, ip := range ips {
				if !publicMCPAddress(ip) {
					return nil, fmt.Errorf("个人MCP不允许访问内网或保留地址")
				}
			}
			var last error
			for _, ip := range ips {
				conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		}
	}
	origin, _ := url.Parse(cfg.URL)
	return &http.Client{Transport: &mcpTransport{base: base, origin: origin}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("MCP端点不允许重定向") }}
}
