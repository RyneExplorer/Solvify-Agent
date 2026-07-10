package dingtalk

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Workspace 描述钉钉知识库
type Workspace struct {
	WorkspaceID string `json:"workspaceId"`
	CorpID      string `json:"corpId"`
	TeamID      string `json:"teamId"`
	RootNodeID  string `json:"rootNodeId"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Icon        Icon   `json:"icon"`
	URL         string `json:"url"`
}

// Icon 描述钉钉知识库图标
type Icon struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Node 描述钉钉知识库节点
type Node struct {
	NodeID      string `json:"nodeId"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Size        int64  `json:"-"`
	Type        string `json:"type"`
	Category    string `json:"category"`
	Extension   string `json:"extension"`
	HasChildren bool   `json:"hasChildren"`
	URL         string `json:"url"`
	ModifiedAt  int64  `json:"-"`
}

// UnmarshalJSON 兼容钉钉 modifiedTime 字符串和数字两种格式
func (n *Node) UnmarshalJSON(data []byte) error {
	type nodeAlias Node
	var input struct {
		*nodeAlias
		Size         dingTalkInt64 `json:"size"`
		ModifiedTime dingTalkInt64 `json:"modifiedTime"`
	}
	input.nodeAlias = (*nodeAlias)(n)
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	n.Size = int64(input.Size)
	n.ModifiedAt = int64(input.ModifiedTime)
	return nil
}

type dingTalkInt64 int64

// UnmarshalJSON 兼容钉钉数字字段返回字符串
func (v *dingTalkInt64) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" || text == `""` {
		*v = 0
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			*v = 0
			return nil
		}
		if parsedAt, err := time.Parse(time.RFC3339, value); err == nil {
			*v = dingTalkInt64(parsedAt.Unix())
			return nil
		}
		if parsedAt, err := time.Parse("2006-01-02T15:04Z07:00", value); err == nil {
			*v = dingTalkInt64(parsedAt.Unix())
			return nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		*v = dingTalkInt64(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return err
	}
	*v = dingTalkInt64(parsed)
	return nil
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
