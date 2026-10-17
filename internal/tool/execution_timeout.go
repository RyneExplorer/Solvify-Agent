package tool

import (
	"context"
	"fmt"
	"time"
)

// ExecutionContext 只传递取消信号，不用后台goroutine伪装工具已经停止。
// 下游IO必须遵守context；副作用工具超时不能推断为未执行。
func ExecutionContext(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if duration <= 0 {
		return nil, nil, fmt.Errorf("工具执行时限必须大于0")
	}
	runCtx, cancel := context.WithTimeout(ctx, duration)
	if err := runCtx.Err(); err != nil {
		cancel()
		return nil, nil, err
	}
	return runCtx, cancel, nil
}

// ConfiguredExecutionTimeout 来自管理员保存的供应商配置，0沿用系统默认。
func ConfiguredExecutionTimeout(seconds int, fallback time.Duration) (time.Duration, error) {
	if seconds < 0 || seconds > 86400 {
		return 0, fmt.Errorf("工具超时时间须为1～86400秒，留空或0沿用系统默认")
	}
	if seconds == 0 {
		return fallback, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

func ExecutionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("工具执行已取消或超时，可能已产生的操作结果需核实: %w", ctx.Err())
	}
	return err
}
