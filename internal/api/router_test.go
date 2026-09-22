package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"solvify-agent/pkg/config"
)

// ─── /metrics 接线回归 ────────────────────────────────────────────────────────
//
// 为什么单独为「一个路由挂了没挂」写测试：/metrics 是运维侧唯一的指标出口，
// 而它的失败形态是**静默**的 —— Registry 没接上时，旧实现返回 200 + 一行注释
// （`#` 是合法的 Prometheus 文本格式），抓取方只会读成「这个服务没有指标」，
// 既没有 404/500，启动期也没有任何告警。于是「接线接错了」在结构上不可发现。
//
// 实测（用真实 NewRouter + httptest）：真实 Registry / 传错类型 / typed nil /
// untyped nil / 根本不传 —— 五种情况**全部 HTTP 200**，唯一差别是 body 里那句注释。
//
// 现在的实现把错误接线提前到编译期（形参有类型），并让「漏注入」这条残留路径
// 以 503 显形。本文件就是钉住这两点。
//
// ⚠️ 断言口径：项目约定业务接口的错误响应 HTTP 恒为 200、只断响应体 code。
// 这里**刻意断 HTTP 状态码**，因为 /metrics 不是业务接口，它由 promhttp 直接服务，
// 抓取方唯一能依据的就是状态码 —— 断 200 正是这个缺陷成立的原因。

const (
	metricsNotWired = "prometheus registry not initialized"
	// 用来验证「真的把 registry 上的指标吐出来了」而不是「恰好没走兜底」。
	probeMetricName = "solvify_metrics_wiring_probe_total"
)

// newTestRouter 用「除 registry 外全 nil」构造真实 Router。
// 前 17 个形参都是接口，nil 合法；用例只打 /metrics，不触碰业务 handler。
func newTestRouter(t *testing.T, reg *prometheus.Registry) *Router {
	t.Helper()
	config.MustLoad("") // Setup 挂的 CORS 中间件要读全局配置
	return NewRouter(
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil,
		reg,
	)
}

func getMetrics(t *testing.T, reg *prometheus.Registry) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	newTestRouter(t, reg).Setup(engine)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w
}

// TestMetricsServesInjectedRegistry 钉住正常接线：注入的 Registry 上的指标必须真的出现在 /metrics。
func TestMetricsServesInjectedRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: probeMetricName,
		Help: "接线探针：注册进去就必须能在 /metrics 上看到",
	})
	reg.MustRegister(c)
	c.Inc()

	w := getMetrics(t, reg)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP=%d，期望 200；body=%q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, probeMetricName) {
		t.Errorf("/metrics 没输出注入 Registry 上的指标 —— Registry 没真正接上。body=%q", body)
	}
	if strings.Contains(body, metricsNotWired) {
		t.Errorf("/metrics 走了未注入兜底分支，说明 registry 形参没生效。body=%q", body)
	}
}

// TestMetricsWithoutRegistryIsLoud 钉住「漏注入必须响亮」。
//
// 反向价值：把兜底改回 `c.String(http.StatusOK, "# ...not initialized\n")`，本用例立刻红。
func TestMetricsWithoutRegistryIsLoud(t *testing.T) {
	w := getMetrics(t, nil)

	if w.Code == http.StatusOK {
		t.Errorf("Registry 为 nil 时仍返回 200 + 占位文本 —— 抓取方会读成「这个服务没有指标」"+
			"而不是「指标没接上」，接线错误无法被告警发现。body=%q", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Registry 为 nil 时 HTTP=%d，期望 503（抓取方据此记 up=0）", w.Code)
	}
	if !strings.Contains(w.Body.String(), metricsNotWired) {
		t.Errorf("响应体没说清失败原因，body=%q", w.Body.String())
	}
}
