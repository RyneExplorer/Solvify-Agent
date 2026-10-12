package config

import (
	"fmt"
	"math"
)

// ContextPolicy 是所有模型调用共享的上下文策略，默认值只在此处定义。
type ContextPolicy struct {
	QuickSoftRatio   float64 `mapstructure:"quick_soft_ratio"`
	QuickTargetRatio float64 `mapstructure:"quick_target_ratio"`
	DeepSoftRatio    float64 `mapstructure:"deep_soft_ratio"`
	DeepTargetRatio  float64 `mapstructure:"deep_target_ratio"`
	MarginRatio      float64 `mapstructure:"margin_ratio"`
	MarginFloor      int     `mapstructure:"margin_floor"`
	OutputLimit      int     `mapstructure:"output_limit"`
}

func DefaultContextPolicy() ContextPolicy {
	return ContextPolicy{QuickSoftRatio: 0.60, QuickTargetRatio: 0.50, DeepSoftRatio: 0.75, DeepTargetRatio: 0.65, MarginRatio: 0.08, MarginFloor: 256, OutputLimit: 4096}
}

// ContextDefaults 供客户端和未加载配置的测试使用同一默认来源。
func ContextDefaults() ContextPolicy {
	if globalConfig != nil {
		return globalConfig.Agent.Context
	}
	return DefaultContextPolicy()
}

func (p ContextPolicy) Validate() error {
	if math.IsNaN(p.QuickSoftRatio+p.QuickTargetRatio+p.DeepSoftRatio+p.DeepTargetRatio+p.MarginRatio) || p.QuickTargetRatio <= 0 || p.QuickTargetRatio >= p.QuickSoftRatio || p.QuickSoftRatio >= 1 || p.DeepTargetRatio <= 0 || p.DeepTargetRatio >= p.DeepSoftRatio || p.DeepSoftRatio >= 1 || p.MarginRatio <= 0 || p.MarginRatio >= 1 || p.MarginFloor <= 0 || p.OutputLimit <= 0 {
		return fmt.Errorf("上下文策略无效：目标水位必须小于软水位，裕量与输出上限必须为正")
	}
	return nil
}
