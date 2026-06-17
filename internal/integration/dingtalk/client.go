package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

const (
	defaultAccessTokenURL = "https://oapi.dingtalk.com/gettoken"
	tokenRefreshWindow    = 5 * time.Minute
)

// Client 封装钉钉开放平台 API 调用能力
type Client struct {
	appKey    string
	appSecret string

	httpClient     *http.Client
	accessTokenURL string

	mu             sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrMsg      string `json:"errmsg"`
	ErrCode     int    `json:"errcode"`
}

// NewClient 创建钉钉开放平台客户端
func NewClient(cfg config.DingTalkConfig) *Client {
	return &Client{
		appKey:         strings.TrimSpace(cfg.AppKey),
		appSecret:      strings.TrimSpace(cfg.AppSecret),
		httpClient:     http.DefaultClient,
		accessTokenURL: defaultAccessTokenURL,
	}
}

// GetAccessToken 获取并缓存钉钉 access_token
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Add(tokenRefreshWindow).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	return c.refreshAccessToken(ctx)
}

// Do 调用钉钉 API 并自动附带 access_token
func (c *Client) Do(ctx context.Context, method, rawURL string, body any, out any) error {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return err
	}

	apiURL, err := url.Parse(rawURL)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "钉钉接口地址格式错误", err)
	}
	query := apiURL.Query()
	query.Set("access_token", token)
	apiURL.RawQuery = query.Encode()

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

// refreshAccessToken 请求钉钉接口刷新 access_token
func (c *Client) refreshAccessToken(ctx context.Context) (string, error) {
	if c.appKey == "" || c.appSecret == "" {
		return "", apperrors.NewDefault(apperrors.CodeDingTalkConfigMissing)
	}

	apiURL, err := url.Parse(c.accessTokenURL)
	if err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "钉钉 access_token 地址格式错误", err)
	}
	query := apiURL.Query()
	query.Set("appkey", c.appKey)
	query.Set("appsecret", c.appSecret)
	apiURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "创建钉钉 access_token 请求失败", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "请求钉钉 access_token 失败", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, fmt.Sprintf("钉钉 access_token 接口返回异常状态: %d", resp.StatusCode))
	}

	var output accessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "解析钉钉 access_token 响应失败", err)
	}
	if output.ErrCode != 0 {
		return "", apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, fmt.Sprintf("获取钉钉 access_token 失败: %s", output.ErrMsg))
	}
	if output.AccessToken == "" {
		return "", apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, "钉钉 access_token 响应为空")
	}

	expiresIn := time.Duration(output.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = tokenRefreshWindow
	}
	c.accessToken = output.AccessToken
	c.tokenExpiresAt = time.Now().Add(expiresIn)
	return c.accessToken, nil
}
