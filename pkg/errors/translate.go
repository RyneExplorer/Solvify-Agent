package errors

import stderrors "errors"

// notFoundSentinels 收集「表示目标记录不存在」的底层哨兵错误。
//
// 由持有该哨兵的层在 init 阶段登记（例：internal/repository 登记 gorm.ErrRecordNotFound），
// 这样本包与 pkg/response 都不必依赖具体 ORM / 驱动 —— 出口只认「已登记的哨兵」。
//
// 为什么需要这一层：持久层的 ErrRecordNotFound 是**实现细节**。它跨过 repository 边界后，
// 上层没有任何标准方式识别它，于是每个调用方各自即兴处置，走向两个相反的极端：
//
//	漏失：原样上抛 → 出口兜底成 500「服务内部错误」（查不到记录返回 500 而非 404）
//	过度补偿：`if err != nil { 一律当"不存在" }` → 数据库故障也被说成「不存在」，
//	          用户看到「会话不存在」就去重建，其实服务已经挂了
//
// 登记 + 出口统一翻译，把「不存在」变成**可判定的事实**，两个极端才都有解。
var notFoundSentinels []error

// RegisterNotFoundSentinel 登记一个「资源不存在」哨兵错误。仅应在 init 阶段调用。
func RegisterNotFoundSentinel(err error) {
	if err == nil {
		return
	}
	notFoundSentinels = append(notFoundSentinels, err)
}

// IsNotFound 判断错误链中是否含「资源不存在」哨兵。
//
// 上层用它区分「目标记录不存在」与「查询本身失败」——
// 前者是 404 / 业务码，后者是 500。缺了这个判据，上层只能二选一全包。
func IsNotFound(err error) bool {
	for _, sentinel := range notFoundSentinels {
		if stderrors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// isFallbackCode 判断是否「原因不明」的兜底码。
//
// 只有 CodeInternalError 是这个语义：调用方用它表示「我没法判断这是什么错误」。
// 具体业务码（如 CodeSessionNotFound）不是兜底，需要原样保留。
func isFallbackCode(code int) bool {
	return code == CodeInternalError
}

// NotFoundOrInternal 把「目标记录不存在」与「查询本身失败」分流成两个业务错误。
//
// 用法（按 id 取单条记录时唯一推荐的写法）：
//
//	v, err := repo.GetByID(ctx, id)
//	if err != nil {
//		return nil, apperrors.NotFoundOrInternal(apperrors.CodeSessionNotFound, err)
//	}
//
// 它把正确写法变成**最省事**的写法：此前每个调用方都只写 `if err != nil` 然后一刀切，
// 于是必然走向某个极端。两种错误都保留原始错误在链上（%w），排障能看到真因。
func NotFoundOrInternal(notFoundCode int, err error) *BizError {
	if IsNotFound(err) {
		return WrapDefault(notFoundCode, err)
	}
	return WrapDefault(CodeInternalError, err)
}

// Translate 把底层错误归一成业务错误。**这是错误出口的唯一翻译点。**
//
// 规则一句话：兜底码让位于已知事实。
// 若调用方把错误统一包成了「服务内部错误」，但错误链里其实躺着一个确定的
// 「记录不存在」哨兵，那就说明调用方当时没能判断出原因 —— 此时以已知事实为准，
// 报 404 而不是 500。
//
// 这样即使某处新代码忘了判断（Go 里 `if err != nil` 之后直接兜底是极自然的写法），
// 也不会再把「查不到记录」退化成一个假的 500。原始错误始终保留在链上（%w），
// 日志与排障不受影响。
//
// ⚠️ 分层约定：新增底层错误类型时，在持有它的包里 RegisterXxx，
// 不要在任何 controller / service 里另写一份 mapping。
//
// ⚠️ 已知边界：若「记录不存在」本身意味着服务器数据不一致（例如会话存在但其属主
// 用户行缺失），这里也会报 404 而非 500 —— 该情形在日志里依然完整可查，
// 但不会计入 5xx 指标。
func Translate(err error) error {
	if err == nil {
		return nil
	}

	var bizErr *BizError
	if stderrors.As(err, &bizErr) {
		if isFallbackCode(bizErr.Code) && IsNotFound(err) {
			return NewWithErr(CodeNotFound, GetMessage(CodeNotFound), err)
		}
		return err
	}

	if IsNotFound(err) {
		return NewWithErr(CodeNotFound, GetMessage(CodeNotFound), err)
	}
	return err
}
