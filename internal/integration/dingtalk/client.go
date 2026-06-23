package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"strings"
	"sync"
	"time"
)

const (
	defaultAccessTokenURL = "https://api.dingtalk.com/v1.0/oauth2/accessToken"
	defaultAPIBaseURL     = "https://api.dingtalk.com"
	tokenRefreshWindow    = 5 * time.Minute
)

// Client 封装钉钉开放平台 API 调用能力
type Client struct {
	appKey    string
	appSecret string

	httpClient     *http.Client
	accessTokenURL string
	apiBaseURL     string

	mu             sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

// NewClient 创建钉钉开放平台客户端
func NewClient(cfg config.DingTalkConfig) *Client {
	return &Client{
		appKey:         strings.TrimSpace(cfg.AppKey),
		appSecret:      strings.TrimSpace(cfg.AppSecret),
		httpClient:     http.DefaultClient,
		accessTokenURL: defaultAccessTokenURL,
		apiBaseURL:     defaultAPIBaseURL,
	}
}

// Do 调用钉钉 API 并自动携带新版 Header 鉴权
func (c *Client) Do(ctx context.Context, method, rawURL string, body any, out any) error {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return err
	}

	apiURL, err := url.Parse(rawURL)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "钉钉接口地址格式错误", err)
	}
	var requestBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "钉钉接口请求体编码失败", err)
		}
		requestBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL.String(), requestBody)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "创建钉钉接口请求失败", err)
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "请求钉钉接口失败", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return apperrors.New(apperrors.CodeDingTalkAPICallFailed, fmt.Sprintf("钉钉接口返回异常状态: %d", resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "解析钉钉接口响应失败", err)
	}
	return nil
}

// apiURL 拼接钉钉开放平台接口地址
func (c *Client) apiURL(apiPath string, values url.Values) string {
	base := strings.TrimRight(c.apiBaseURL, "/")
	rawURL := base + apiPath
	if len(values) > 0 {
		rawURL += "?" + values.Encode()
	}
	return rawURL
}

// downloadResource 按钉钉返回的签名 Header 下载文件
func (c *Client) downloadResource(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "创建钉钉文件下载请求失败", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "下载钉钉文件失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, apperrors.New(apperrors.CodeDingTalkAPICallFailed, fmt.Sprintf("钉钉文件下载返回异常状态: %d", resp.StatusCode))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "读取钉钉文件内容失败", err)
	}
	return data, nil
}
