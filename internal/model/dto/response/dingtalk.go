package response

// DingTalkOAuthConfigResponse 描述前端内嵌二维码所需参数
type DingTalkOAuthConfigResponse struct {
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
	Scope        string `json:"scope"`
	ResponseType string `json:"response_type"`
	Prompt       string `json:"prompt"`
	State        string `json:"state"`
}

// DingTalkBindingResponse 描述当前用户钉钉绑定状态
type DingTalkBindingResponse struct {
	Bound       bool   `json:"bound"`
	DingOpenID  string `json:"ding_open_id,omitempty"`
	DingUnionID string `json:"ding_union_id,omitempty"`
	CorpID      string `json:"corp_id,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
}

// DingTalkWorkspaceResponse 描述钉钉知识库响应
type DingTalkWorkspaceResponse struct {
	WorkspaceID string `json:"workspace_id"`
	RootNodeID  string `json:"root_node_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
}

// DingTalkNodeResponse 描述钉钉知识库节点响应
type DingTalkNodeResponse struct {
	NodeID      string `json:"node_id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	ModifiedAt  int64  `json:"modified_at"`
}

// DingTalkWorkspaceListResponse 描述钉钉知识库列表响应
type DingTalkWorkspaceListResponse struct {
	Workspaces []DingTalkWorkspaceResponse `json:"workspaces"`
	NextToken  string                      `json:"next_token"`
}

// DingTalkNodeListResponse 描述钉钉节点列表响应
type DingTalkNodeListResponse struct {
	Nodes     []DingTalkNodeResponse `json:"nodes"`
	NextToken string                 `json:"next_token"`
}
