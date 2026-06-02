// Package pipeline 中的 concurrency.go 解析 step 级跨主机执行策略。
//
// 职责：
//   - 将 serial/parallel/batch:N 转成引擎可执行配置
//   - 为危险部署操作提供默认 serial 语义
//
// 边界：
//   - 不调度任务，不执行插件
//   - 不兼容旧的分离式并发字段
package pipeline

import (
	"fmt"
	"strconv"
	"strings"
)

// ConcurrencyMode 表示 step 对多个 target 的执行模式。
type ConcurrencyMode string

const (
	// ConcurrencySerial 逐台执行，遇错即停。
	ConcurrencySerial ConcurrencyMode = "serial"
	// ConcurrencyParallel 全部 target 并发，跑完后汇总错误。
	ConcurrencyParallel ConcurrencyMode = "parallel"
	// ConcurrencyBatch 每批 N 台并发，批间串行。
	ConcurrencyBatch ConcurrencyMode = "batch"
)

// StepConcurrency 是解析后的 step 并发配置。
type StepConcurrency struct {
	Mode  ConcurrencyMode
	Limit int
}

// ParseStepConcurrency 解析 step.concurrency，空值默认 serial。
func ParseStepConcurrency(value string) (StepConcurrency, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == string(ConcurrencySerial) {
		return StepConcurrency{Mode: ConcurrencySerial, Limit: 1}, nil
	}
	if value == string(ConcurrencyParallel) {
		return StepConcurrency{Mode: ConcurrencyParallel, Limit: 0}, nil
	}
	if strings.HasPrefix(value, "batch:") {
		raw := strings.TrimPrefix(value, "batch:")
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return StepConcurrency{}, fmt.Errorf("invalid concurrency %q", value)
		}
		return StepConcurrency{Mode: ConcurrencyBatch, Limit: n}, nil
	}
	return StepConcurrency{}, fmt.Errorf("invalid concurrency %q", value)
}
