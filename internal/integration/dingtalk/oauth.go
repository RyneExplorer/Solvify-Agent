package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	apperrors "solvify-agent/pkg/errors"
)

// ExchangeUserAccessToken 使用扫码授权码换取用户个人访问凭证
func (c *Client) ExchangeUserAccessToken(ctx context.Context, authCode string) (UserAccessToken, error) {
	if c.appKey == "" || c.appSecret == "" {
		return UserAccessToken{}, apperrors.NewDefault(apperrors.CodeDingTalkConfigMissing)
	}
	body := userAccessTokenRequest{
		ClientID:     c.appKey,
		ClientSecret: c.appSecret,
		Code:         strings.TrimSpace(authCode),
		GrantType:    "authorization_code",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return UserAccessToken{}, apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "钉钉用户 token 请求体编码失败", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("/v1.0/oauth2/userAccessToken", nil), bytes.NewReader(data))
	if err != nil {
		return UserAccessToken{}, apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "创建钉钉用户 token 请求失败", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UserAccessToken{}, apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "请求钉钉用户 token 失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UserAccessToken{}, apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, fmt.Sprintf("钉钉用户 token 接口返回异常状态: %d", resp.StatusCode))
	}
	var output UserAccessToken
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return UserAccessToken{}, apperrors.NewWithErr(apperrors.CodeDingTalkAccessTokenFailed, "解析钉钉用户 token 响应失败", err)
	}
	if output.AccessToken == "" {
		return UserAccessToken{}, apperrors.New(apperrors.CodeDingTalkAccessTokenFailed, "钉钉用户 token 响应为空")
	}
	return output, nil
}

// GetCurrentUserInfo 获取扫码授权用户个人信息
func (c *Client) GetCurrentUserInfo(ctx context.Context, userAccessToken string) (UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("/v1.0/contact/users/me", nil), nil)
	if err != nil {
		return UserInfo{}, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "创建钉钉用户信息请求失败", err)
	}
	req.Header.Set("x-acs-dingtalk-access-token", strings.TrimSpace(userAccessToken))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UserInfo{}, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "请求钉钉用户信息失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UserInfo{}, apperrors.New(apperrors.CodeDingTalkAPICallFailed, fmt.Sprintf("钉钉用户信息接口返回异常状态: %d", resp.StatusCode))
	}
	var output UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
		return UserInfo{}, apperrors.NewWithErr(apperrors.CodeDingTalkAPICallFailed, "解析钉钉用户信息响应失败", err)
	}
	if output.UnionID == "" {
		return UserInfo{}, apperrors.New(apperrors.CodeDingTalkAPICallFailed, "钉钉用户 unionId 为空")
	}
	return output, nil
}
