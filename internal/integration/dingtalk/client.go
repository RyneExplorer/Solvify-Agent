package dingtalk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrMsg      string `json:"errmsg"`
	ErrCode     int    `json:"errcode"`
}

// Workspace 描述钉钉知识库
type Workspace struct {
	WorkspaceID string `json:"workspaceId"`
	CorpID      string `json:"corpId"`
	TeamID      string `json:"teamId"`
	RootNodeID  string `json:"rootNodeId"`
	Name        string `json:"name"`
	Type        string `json:"type"`
}

// Node 描述钉钉知识库节点
type Node struct {
	NodeID      string `json:"nodeId"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	ModifiedAt  int64  `json:"modifiedTime"`
}

// DentryInfo 描述钉钉存储文件标识
type DentryInfo struct {
	DentryUUID string `json:"dentryUuid"`
	DentryID   string `json:"dentryId"`
	SpaceID    string `json:"spaceId"`
}

type workspaceOutput struct {
	Workspace Workspace `json:"workspace"`
}

type workspaceListOutput struct {
	Workspaces []Workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

type nodeOutput struct {
	Node Node `json:"node"`
}

type nodeListOutput struct {
	Nodes     []Node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

type downloadInfoOutput struct {
	Protocol            string              `json:"protocol"`
	HeaderSignatureInfo headerSignatureInfo `json:"headerSignatureInfo"`
}

type headerSignatureInfo struct {
	ResourceURLs []string          `json:"resourceUrls"`
	Headers      map[string]string `json:"headers"`
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

// GetAccessToken 获取并缓存钉钉 access_token
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Add(tokenRefreshWindow).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	return c.refreshAccessToken(ctx)
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

// GetWorkspace 获取单个钉钉知识库
func (c *Client) GetWorkspace(ctx context.Context, operatorID, workspaceID string) (Workspace, error) {
	values := url.Values{}
	values.Set("withPermissionRole", "false")
	values.Set("operatorId", strings.TrimSpace(operatorID))
	var output workspaceOutput
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/wiki/workspaces/"+url.PathEscape(workspaceID), values), nil, &output)
	return output.Workspace, err
}

// ListWorkspaces 分页获取钉钉知识库列表
func (c *Client) ListWorkspaces(ctx context.Context, operatorID, nextToken string, maxResults int) ([]Workspace, string, error) {
	values := url.Values{}
	values.Set("operatorId", strings.TrimSpace(operatorID))
	values.Set("withPermissionRole", "false")
	if nextToken != "" {
		values.Set("nextToken", nextToken)
	}
	if maxResults <= 0 || maxResults > 30 {
		maxResults = 30
	}
	values.Set("maxResults", fmt.Sprintf("%d", maxResults))
	var output workspaceListOutput
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/wiki/workspaces", values), nil, &output)
	return output.Workspaces, output.NextToken, err
}

// GetMineWorkspace 获取我的文档知识库信息
func (c *Client) GetMineWorkspace(ctx context.Context, operatorID string) (Workspace, error) {
	values := url.Values{}
	values.Set("operatorId", strings.TrimSpace(operatorID))
	var output workspaceOutput
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/wiki/mineWorkspaces", values), nil, &output)
	return output.Workspace, err
}

// GetNode 获取单个知识库节点
func (c *Client) GetNode(ctx context.Context, operatorID, nodeID string) (Node, error) {
	values := url.Values{}
	values.Set("operatorId", strings.TrimSpace(operatorID))
	values.Set("withStatisticalInfo", "false")
	values.Set("withPermissionRole", "false")
	var output nodeOutput
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/wiki/nodes/"+url.PathEscape(nodeID), values), nil, &output)
	return output.Node, err
}

// ListNodes 分页获取知识库节点列表
func (c *Client) ListNodes(ctx context.Context, operatorID, parentNodeID, nextToken string, maxResults int) ([]Node, string, error) {
	values := url.Values{}
	values.Set("operatorId", strings.TrimSpace(operatorID))
	values.Set("parentNodeId", parentNodeID)
	values.Set("withPermissionRole", "false")
	if nextToken != "" {
		values.Set("nextToken", nextToken)
	}
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 50
	}
	values.Set("maxResults", fmt.Sprintf("%d", maxResults))
	var output nodeListOutput
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/wiki/nodes", values), nil, &output)
	return output.Nodes, output.NextToken, err
}

// QueryDentryID 根据 dentryUuid 查询 spaceId 和 dentryId
func (c *Client) QueryDentryID(ctx context.Context, operatorID, dentryUUID string) (DentryInfo, error) {
	values := url.Values{}
	values.Set("operatorId", strings.TrimSpace(operatorID))
	var output DentryInfo
	err := c.Do(ctx, http.MethodGet, c.apiURL("/v2.0/doc/dentries/"+url.PathEscape(dentryUUID)+"/queryDentryId", values), nil, &output)
	return output, err
}

// DownloadFile 下载钉钉知识库文件内容
func (c *Client) DownloadFile(ctx context.Context, operatorID, spaceID, dentryID string) ([]byte, string, error) {
	values := url.Values{}
	values.Set("unionId", strings.TrimSpace(operatorID))
	rawPath := "/v1.0/storage/spaces/" + url.PathEscape(spaceID) + "/dentries/" + url.PathEscape(dentryID) + "/downloadInfos/query"
	body := map[string]any{
		"option": map[string]any{
			"version":        1,
			"preferIntranet": false,
		},
	}
	var output downloadInfoOutput
	if err := c.Do(ctx, http.MethodPost, c.apiURL(rawPath, values), body, &output); err != nil {
		return nil, "", err
	}
	if len(output.HeaderSignatureInfo.ResourceURLs) == 0 {
		return nil, "", apperrors.New(apperrors.CodeDingTalkAPICallFailed, "钉钉文件下载地址为空")
	}
	data, err := c.downloadResource(ctx, output.HeaderSignatureInfo.ResourceURLs[0], output.HeaderSignatureInfo.Headers)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
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
