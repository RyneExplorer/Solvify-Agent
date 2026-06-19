package request

// DingTalkAuthCodeExchangeRequest 描述钉钉扫码授权码兑换请求
type DingTalkAuthCodeExchangeRequest struct {
	AuthCode string `json:"auth_code" binding:"required"`
	State    string `json:"state" binding:"required"`
}
