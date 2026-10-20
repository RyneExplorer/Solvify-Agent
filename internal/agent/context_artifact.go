package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

const artifactReadToolName = "read_context_artifact"

func (e *Engine) WithArtifactRepo(repo repository.ContextArtifactRepo) { e.artifactRepo = repo }

type artifactReference struct {
	OwnerTaskID  string             `json:"owner_task_id,omitempty"`
	OwnerAgentID string             `json:"owner_agent_id,omitempty"`
	ArtifactID   string             `json:"artifact_id"`
	Hash         string             `json:"hash"`
	Version      int                `json:"version"`
	Runes        int                `json:"runes"`
	Preview      string             `json:"preview"`
	Read         string             `json:"read"`
	MaxReadRunes int                `json:"max_read_runes,omitempty"`
	NextOffset   int                `json:"next_offset,omitempty"`
	Content      string             `json:"content,omitempty"`
	Complete     bool               `json:"complete"`
	Selection    *artifactSelection `json:"selection,omitempty"`
	Offset       int                `json:"offset,omitempty"`
	TotalRunes   int                `json:"total_runes,omitempty"`
}

// 选择器及偏移均由程序记录；分页偏移相对于所选视图，原文始终保持不变。
type artifactSelection struct {
	DecodedText bool   `json:"decoded_text,omitempty"`
	Kind        string `json:"kind"`
	Value       string `json:"value,omitempty"`
	SourceStart int    `json:"source_start,omitempty"`
}

func contentHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func (e *Engine) runtimePolicy() config.RuntimeContextPolicy {
	if e.cfg.RuntimeContext.TTLHours == 0 {
		return config.DefaultRuntimeContextPolicy()
	}
	return e.cfg.RuntimeContext
}

func artifactScope(req Request) repository.ArtifactScope {
	return repository.ArtifactScope{UserID: req.UserID, SessionID: req.SessionID, RunID: req.RunID, AgentID: req.AgentName, TaskID: req.TaskID}
}

func (e *Engine) artifactMiddleware(req Request) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
			out, err := next(ctx, in)
			if err == nil && out != nil && in.Name != artifactReadToolName {
				copy := *out
				copy.Result = citationToolResult(req, in.Name, out.Result)
				out = &copy
			}
			if err != nil || out == nil || e.artifactRepo == nil || in.Name == artifactReadToolName {
				return out, err
			}
			// 拒绝、审批和明确失败结果保留原样，不让预览覆盖执行事实。
			if protectedToolResult(out.Result) || e.clarifyToolNames()[in.Name] || e.dangerousToolNames(ctx, nil)[in.Name] || isDangerousDynamicTool(in.Name) {
				return out, nil
			}
			p := e.runtimePolicy()
			if tokenutil.CountTokens(out.Result, req.ModelName) <= p.ArtifactThreshold {
				return out, nil
			}
			scope := artifactScope(req)
			hash := contentHash(out.Result)
			id := contentHash(scope.UserID + "\x00" + scope.SessionID + "\x00" + scope.RunID + "\x00" + scope.AgentID + "\x00" + scope.TaskID + "\x00" + in.CallID + "\x00" + hash)
			a := &entity.ContextArtifact{ID: id, UserID: scope.UserID, SessionID: scope.SessionID, RunID: scope.RunID, AgentID: scope.AgentID, TaskID: scope.TaskID, ToolCallID: in.CallID, Content: out.Result, Hash: hash, Bytes: int64(len(out.Result)), Version: 1, CreatedAt: time.Now(), ExpiredAt: time.Now().Add(time.Duration(p.TTLHours) * time.Hour)}
			if err = e.artifactRepo.SaveImmutable(ctx, a, repository.ArtifactLimits{ArtifactBytes: p.ArtifactBytes, RunBytes: p.RunBytes, UserBytes: p.UserBytes}); err != nil {
				logger.Warn("上下文产物保存失败，停止本次结果处理")
				return nil, fmt.Errorf("工具原文保存失败，无法可靠读取完整结果: %w", err)
			}
			preview, _ := tokenutil.TruncateByTokens(out.Result, req.ModelName, min(p.ReadTokens, 500))
			readPolicy := e.artifactReadingPolicy(req)
			raw, _ := json.Marshal(artifactReference{
				OwnerTaskID: scope.TaskID, OwnerAgentID: scope.AgentID,
				ArtifactID: id, Hash: hash, Version: 1, Runes: len([]rune(out.Result)), Preview: preview,
				MaxReadRunes: readPolicy.ReadRunes,
				Read:         fmt.Sprintf("原文已保存；预览不代表全部内容。可使用read_context_artifact按keyword、section或json_path定位；分段每次最多%d个字符，超大limit自动缩小。实际长度还受动态token预算限制；complete=false时用返回的next_offset继续，不要按请求limit推算偏移。", readPolicy.ReadRunes),
			})
			copy := *out
			copy.Result = string(raw)
			return &copy, nil
		}
	}
}

func protectedToolResult(content string) bool {
	if strings.Contains(content, "拒绝") || strings.Contains(content, "未执行") {
		return true
	}
	return classifyToolResult(content).Failed
}

type artifactReadTool struct {
	repo   repository.ContextArtifactRepo
	scope  repository.ArtifactScope
	policy config.RuntimeContextPolicy
	model  string
	grants map[string]artifactReadGrant
}

type artifactArgumentFeedback struct {
	Success      bool   `json:"success"`
	ErrorType    string `json:"error_type"`
	Field        string `json:"field"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	MaxReadRunes int    `json:"max_read_runes"`
}

func (t artifactReadTool) invalidArguments(field, message string) (string, error) {
	raw, err := json.Marshal(artifactArgumentFeedback{
		ErrorType: "invalid_arguments", Field: field, Message: message,
		Retryable: true, MaxReadRunes: t.policy.ReadRunes,
	})
	return string(raw), err
}

// 仅识别程序内置读取工具的参数反馈，外部工具不能声明此标记以清除失败事实。
func artifactArgumentFailure(toolName, result string) (string, bool) {
	if toolName != artifactReadToolName {
		return "", false
	}
	var feedback artifactArgumentFeedback
	if json.Unmarshal([]byte(result), &feedback) != nil || feedback.Success || !feedback.Retryable || feedback.ErrorType != "invalid_arguments" || feedback.Message == "" {
		return "", false
	}
	return feedback.Message, true
}

func (t artifactReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: artifactReadToolName,
		Desc: fmt.Sprintf("读取本任务产物及程序授权的前置依赖产物；keyword定位关键词，section定位Markdown章节，json_path用JSON Pointer定位字段，chunk_id定位知识分块。json_path可与keyword组合：先展开字段再定位；其余定位参数不组合。默认读取原文，偏移/长度以字符计，每次最多%d字符且受token预算限制；complete表示所选视图是否读到末尾，false用next_offset续读。只能用工具给出的artifact_id。invalid_arguments按提示纠正，纠错消耗当前执行预算。", t.policy.ReadRunes),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"artifact_id": {Type: schema.String, Required: true, Desc: "工具结果提供的产物ID"},
			"offset":      {Type: schema.Integer, Desc: "从0开始的字符偏移；省略为0，后续分页使用上次返回的next_offset"},
			"keyword":     {Type: schema.String, Desc: "在offset之后定位关键词，返回从命中位置开始的原文；后续保持返回的selection视图，不带keyword并使用next_offset续读"},
			"section":     {Type: schema.String, Desc: "Markdown章节标题，含其子章节；续读时保留section并使用next_offset"},
			"json_path":   {Type: schema.String, Desc: "JSON Pointer，例如/sources/0/content或/data/items；字符串按解码正文偏移，其他值按JSON序列化字符偏移，续读时保留路径"},
			"chunk_id":    {Type: schema.String, Desc: "知识库sources中的真实分块id；返回该分块完整记录，续读时保留chunk_id"},
			"limit":       {Type: schema.Integer, Desc: fmt.Sprintf("请求读取的字符数；省略或0默认%d，超过%d自动缩小；实际返回可能因token预算进一步减少", t.policy.ReadRunes, t.policy.ReadRunes)},
		}),
	}, nil
}

func (t artifactReadTool) InvokableRun(ctx context.Context, args string, _ ...einoTool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if value, ok := adk.GetSessionValue(ctx, artifactReadBudgetKey(t.scope)); ok {
		if allowance, valid := value.(int); valid {
			t.policy.ReadTokens = min(t.policy.ReadTokens, max(1, allowance))
		}
	}
	var req struct {
		ID       string  `json:"artifact_id"`
		Offset   int     `json:"offset"`
		Limit    int     `json:"limit"`
		Keyword  string  `json:"keyword"`
		Section  string  `json:"section"`
		JSONPath *string `json:"json_path"`
		ChunkID  string  `json:"chunk_id"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		var fieldErr *json.UnmarshalTypeError
		if errors.As(err, &fieldErr) {
			switch fieldErr.Field {
			case "artifact_id":
				return t.invalidArguments("artifact_id", "artifact_id必须是工具结果提供的字符串ID，请修正后重新调用")
			case "offset", "limit":
				return t.invalidArguments(fieldErr.Field, fieldErr.Field+"必须是非负整数，请修正后重新调用")
			}
		}
		return t.invalidArguments("arguments", "参数必须是合法JSON对象，字段为artifact_id（字符串）、offset和limit（非负整数），请修正后重新调用")
	}
	if _, err := hex.DecodeString(req.ID); err != nil || len(req.ID) != 64 {
		return t.invalidArguments("artifact_id", "artifact_id必须是工具结果提供的64位十六进制ID，请勿自行生成，请修正后重新调用")
	}
	if req.Offset < 0 {
		return t.invalidArguments("offset", "offset不能为负数；首次读取使用0，后续使用返回的next_offset，请修正后重新调用")
	}
	if req.Limit < 0 {
		return t.invalidArguments("limit", "limit不能为负数；省略或设为0使用配置上限，请修正后重新调用")
	}
	selectors := 0
	for _, selected := range []bool{req.Keyword != "", req.Section != "", req.JSONPath != nil, req.ChunkID != ""} {
		if selected {
			selectors++
		}
	}
	if selectors > 1 && !(selectors == 2 && req.JSONPath != nil && req.Keyword != "") {
		return t.invalidArguments("arguments", "仅json_path可与keyword组合，其他定位参数最多选择一个")
	}
	// limit是请求的页长；缩小超大请求即可继续读取，无需终止任务或额外纠错调用。
	if req.Limit == 0 || req.Limit > t.policy.ReadRunes {
		req.Limit = t.policy.ReadRunes
	}
	a, err := findArtifactWithGrants(ctx, t.repo, t.scope, t.grants, req.ID)
	if err != nil {
		return "", err
	}
	if a.Version != 1 || contentHash(a.Content) != a.Hash {
		return "", fmt.Errorf("上下文产物版本或哈希不匹配")
	}
	content := a.Content
	selection := artifactSelection{Kind: "source"}
	// MCP正文是JSON字符串；在解码后的正文中定位，偏移也固定到该视图。
	if req.JSONPath == nil && req.Keyword != "" {
		if path, found := artifactTextPath(content, req.Keyword); found {
			req.JSONPath = &path
		}
	}
	if req.JSONPath != nil {
		content, err = artifactJSONPointerView(content, *req.JSONPath, true)
		if err != nil {
			return t.invalidArguments("json_path", err.Error())
		}
		selection.Kind, selection.Value, selection.DecodedText = "json_path", *req.JSONPath, true
	}
	if req.ChunkID != "" {
		content, err = artifactKnowledgeChunk(content, req.ChunkID)
		if err != nil {
			return t.invalidArguments("chunk_id", err.Error())
		}
		selection.Kind, selection.Value = "chunk_id", req.ChunkID
	}
	if req.Section != "" {
		start, end, found := artifactSection(content, req.Section)
		if !found {
			return t.invalidArguments("section", "未找到该章节，请使用keyword查找标题或读取目录")
		}
		content = string([]rune(content)[start:end])
		selection.Kind, selection.Value, selection.SourceStart = "section", req.Section, start
	}
	runes := []rune(content)
	if req.Offset > len(runes) {
		return t.invalidArguments("offset", fmt.Sprintf("offset超出范围，允许0至%d；请使用上次返回的next_offset或从0开始重新读取", len(runes)))
	}
	if req.Keyword != "" {
		position, found := locateArtifact(content, req.Keyword, req.Offset)
		if !found {
			return t.invalidArguments("keyword", fmt.Sprintf("当前位置之后未找到关键词；请从offset=0换用短关键词，或用json_path定位正文后续读。当前视图长度%d；不要猜测偏移。", len(runes)))
		}
		req.Offset = position
	}
	end := req.Offset + min(len(runes)-req.Offset, req.Limit)
	// 按完整字符缩短，并以最终JSON计费，避免转义膨胀、短末页空转和半个UTF-8字符。
	// 搜索范围受ReadRunes限制；非末尾至少返回一个字符，否则明确报告额度不足。
	encodePage := func(size int) string {
		next := req.Offset + size
		raw, _ := json.Marshal(artifactReference{OwnerTaskID: a.TaskID, OwnerAgentID: a.AgentID, ArtifactID: a.ID, Hash: a.Hash, Version: a.Version, Runes: len([]rune(a.Content)), Offset: req.Offset, NextOffset: next, TotalRunes: len(runes), Content: string(runes[req.Offset:next]), Complete: next == len(runes), MaxReadRunes: t.policy.ReadRunes, Selection: &selection})
		return string(raw)
	}
	if full := encodePage(end - req.Offset); tokenutil.CountTokens(full, t.model) <= t.policy.ReadTokens {
		return full, nil
	}
	low, high := min(1, end-req.Offset), end-req.Offset-1
	var result string
	for low <= high {
		size := low + (high-low)/2
		next := req.Offset + size
		piece := string(runes[req.Offset:next])
		raw, err := json.Marshal(artifactReference{OwnerTaskID: a.TaskID, OwnerAgentID: a.AgentID, ArtifactID: a.ID, Hash: a.Hash, Version: a.Version, Runes: len([]rune(a.Content)), Offset: req.Offset, NextOffset: next, TotalRunes: len(runes), Content: piece, Complete: next == len(runes), MaxReadRunes: t.policy.ReadRunes, Selection: &selection})
		if err != nil {
			return "", err
		}
		if tokenutil.CountTokens(string(raw), t.model) <= t.policy.ReadTokens {
			result = string(raw)
			low = size + 1
		} else {
			high = size - 1
		}
	}
	if result != "" {
		return result, nil
	}
	return "", fmt.Errorf("产物读取额度不足，无法返回有效片段")
}

func (e *Engine) addArtifactTool(req Request, tools []einoTool.BaseTool) []einoTool.BaseTool {
	if e.artifactRepo == nil || req.AgentName == "synthesis_review" {
		return tools
	}
	return append(append([]einoTool.BaseTool(nil), tools...), artifactReadTool{repo: e.artifactRepo, scope: artifactScope(req), policy: e.artifactReadingPolicy(req), model: req.ModelName, grants: req.artifactReads})
}

func appendArtifactReference(refs []artifactReference, ref artifactReference) []artifactReference {
	if ref.Selection == nil && ref.TotalRunes > 0 {
		ref.Selection = &artifactSelection{Kind: "source"}
	}
	// 任务结果中的引用只存定位信息，不在结果与摘要引用中重复复制原文。
	ref.Content, ref.Preview = "", ""
	for _, current := range refs {
		if current.ArtifactID == ref.ArtifactID && sameArtifactSelection(current.Selection, ref.Selection) && current.Offset == ref.Offset && current.NextOffset == ref.NextOffset {
			return refs
		}
	}
	return append(refs, ref)
}

// taskArtifactEvidence reads only program-recorded dependencies, under their original task scope.
// Synthesis receives bounded evidence data and gains no operation or arbitrary read tool.
func (e *Engine) taskArtifactEvidence(ctx context.Context, req Request, task TaskSpec, results map[string]TaskResult) (string, error) {
	type evidence struct {
		ArtifactID, Hash, TaskID, Content string
		Version, TotalRunes               int
		Complete                          bool
		Selection                         *artifactSelection `json:",omitempty"`
		Offset, NextOffset                int
	}
	var messages []*schema.Message
	var taskIDs []string
	dynamic := false
	remaining := e.runtimePolicy().SummaryInput
	if _, err := requestContextBudget(req); err == nil {
		dynamic = true
		remaining = e.artifactReadingPolicy(req).ReadTokens
	}
	for _, dep := range task.DependsOn {
		result := results[dep]
		if result.Status != TaskSuccess {
			continue
		}
		selected := map[string]bool{}
		for _, ref := range result.Artifacts {
			if ref.Selection != nil {
				selected[ref.ArtifactID] = true
			}
		}
		for _, ref := range result.Artifacts {
			if ref.Selection == nil && selected[ref.ArtifactID] {
				continue
			}
			if e.artifactRepo == nil {
				return "", fmt.Errorf("上下文产物存储不可用")
			}
			scopeReq := req
			scopeReq.TaskID, scopeReq.AgentName = artifactOwner(ref, result)
			owner, allowed := results[scopeReq.TaskID]
			if !allowed || owner.Status != TaskSuccess || owner.Agent != scopeReq.AgentName {
				return "", fmt.Errorf("产物不属于有效前置任务")
			}
			a, err := e.artifactRepo.FindScoped(ctx, artifactScope(scopeReq), ref.ArtifactID)
			if err != nil {
				return "", err
			}
			if a.Hash != ref.Hash || a.Version != ref.Version || contentHash(a.Content) != a.Hash {
				return "", fmt.Errorf("研究证据版本或哈希不匹配")
			}
			view, err := artifactEvidenceView(a.Content, ref)
			if err != nil {
				return "", err
			}
			ref.Content, ref.Preview, ref.Runes = view, "", len([]rune(a.Content))
			if ref.Selection == nil {
				ref.NextOffset = ref.Runes
			}
			ref.Complete = ref.Selection == nil || ref.Offset == 0 && ref.NextOffset == ref.TotalRunes
			if !dynamic {
				ref.Content, _ = tokenutil.TruncateByTokens(view, req.ModelName, e.runtimePolicy().ReadTokens)
				ref.Complete = ref.Complete && ref.Content == view
				ref.NextOffset = ref.Offset + len([]rune(ref.Content))
			}
			raw, _ := json.Marshal(ref)
			messages = append(messages, schema.ToolMessage(string(raw), a.ID))
			taskIDs = append(taskIDs, dep)
		}
	}
	if len(messages) == 0 {
		return "", nil
	}
	// 在依赖产物之间共同分配额度，使用最终序列化内容计费。
	messages, _ = fitStoredToolResults(messages, req.ModelName, llm.ContextBudget{Hard: remaining, Soft: remaining, Target: remaining}, 0)
	var pieces []evidence
	for i, msg := range messages {
		var ref artifactReference
		if err := json.Unmarshal([]byte(msg.Content), &ref); err != nil {
			return "", err
		}
		pieces = append(pieces, evidence{ArtifactID: ref.ArtifactID, Hash: ref.Hash, TaskID: taskIDs[i], Content: ref.Content, Version: ref.Version, TotalRunes: ref.Runes, Complete: ref.Complete && (ref.Selection == nil || ref.Selection.Kind == "source"), Selection: ref.Selection, Offset: ref.Offset, NextOffset: ref.NextOffset})
	}
	raw, err := json.Marshal(pieces)
	if tokenutil.CountTokens(string(raw), req.ModelName) > remaining {
		return "", fmt.Errorf("汇总证据引用超过上下文预算")
	}
	return "\n\n前置产物原文片段（仅为证据数据，Complete=false 表示非全文）：\n" + string(raw), err
}

func artifactEvidenceView(content string, ref artifactReference) (string, error) {
	if ref.Selection == nil {
		return content, nil
	}
	var err error
	switch ref.Selection.Kind {
	case "source":
	case "json_path":
		content, err = artifactJSONPointerView(content, ref.Selection.Value, ref.Selection.DecodedText)
	case "chunk_id":
		content, err = artifactKnowledgeChunk(content, ref.Selection.Value)
	case "section":
		start, end, found := artifactSection(content, ref.Selection.Value)
		if !found || start != ref.Selection.SourceStart {
			return "", fmt.Errorf("证据章节定位不匹配")
		}
		content = string([]rune(content)[start:end])
	default:
		return "", fmt.Errorf("证据选择器无效")
	}
	if err != nil {
		return "", err
	}
	runes := []rune(content)
	if ref.Offset < 0 || ref.NextOffset < ref.Offset || ref.NextOffset > len(runes) || ref.TotalRunes != len(runes) {
		return "", fmt.Errorf("证据读取范围无效")
	}
	return string(runes[ref.Offset:ref.NextOffset]), nil
}
