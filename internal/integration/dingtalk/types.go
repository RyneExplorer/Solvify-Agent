package dingtalk

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

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int    `json:"expireIn"`
}

type userAccessTokenRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Code         string `json:"code"`
	GrantType    string `json:"grantType"`
}

// UserAccessToken 描述钉钉用户个人访问凭证
type UserAccessToken struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpireIn     int    `json:"expireIn"`
	CorpID       string `json:"corpId"`
}

// UserInfo 描述钉钉当前授权用户信息
type UserInfo struct {
	Nick      string `json:"nick"`
	AvatarURL string `json:"avatarUrl"`
	Mobile    string `json:"mobile"`
	OpenID    string `json:"openId"`
	UnionID   string `json:"unionId"`
	Email     string `json:"email"`
}
