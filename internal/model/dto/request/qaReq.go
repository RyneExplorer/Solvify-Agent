package request

// AskRequest 描述问答请求参数
type AskRequest struct {
	Question string `json:"question"`
	UseRAG   bool   `json:"use_rag"`
	UseTools bool   `json:"use_tools"`
}
