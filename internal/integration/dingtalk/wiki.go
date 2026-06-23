package dingtalk

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

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
