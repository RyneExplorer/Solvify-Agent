package repository

import (
	"gorm.io/gorm"

	apperrors "solvify-agent/pkg/errors"
)

// 把持久层的哨兵错误登记给错误出口。
//
// 「记录不存在」是数据访问层的事实，但它必须能被 repository 之上所有层识别 ——
// 否则上层只能靠 `if err != nil` 一刀切，要么把任何错误都说成「不存在」，
// 要么把它兜成 500。登记之后，上层用 apperrors.IsNotFound(err) 判断，
// 出口（pkg/response.BizError）自动翻译成 404。
//
// 放在 init 里而不是显式调用：该事实与「本包存在」是同一件事，
// 任何用到 repository 的进程都必然带上它，不依赖启动代码记得调一次。
func init() {
	apperrors.RegisterNotFoundSentinel(gorm.ErrRecordNotFound)
}
