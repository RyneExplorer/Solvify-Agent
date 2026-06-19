package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	apperrors "solvify-agent/pkg/errors"
)

// GetAccessToken 获取并缓存钉钉 access_token
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Add(tokenRefreshWindow).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	return c.refreshAccessToken(ctx)
}

// refreshAccessToken 请求钉钉接口刷新 access_token
func (c *Client) refreshAccessToken(ctx context.Context) (string, error) {
	if c.appKey == "" || c.appSecret == "" {
		return "", apperrors.NewDefault(apperrors.CodeDingTalkConfigMissing)
	}

	body := map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "钉钉 access_token 请求体编码失败", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.accessTokenURL, bytes.NewReader(data))
	if err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "创建钉钉 access_token 请求失败", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
	if output.AccessToken == "" {
		return "", apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, "钉钉 access_token 响应为空")
	}

	expiresIn := time.Duration(output.ExpireIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = tokenRefreshWindow
	}
	c.accessToken = output.AccessToken
	c.tokenExpiresAt = time.Now().Add(expiresIn)
	return c.accessToken, nil
}
