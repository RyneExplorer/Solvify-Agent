package dingtalk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	apperrors "solvify-agent/pkg/errors"
)

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
