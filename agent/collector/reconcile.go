// Package collector 提供远端日志采集任务的声明式 reconcile 能力。
//
// 职责：
//   - 将 managed deployment 清单投影成 collector 期望状态
//   - 只启停由 managed reconcile 接管的 collector，保留手动/旧接口启动的 collector
//
// 边界：
//   - 不读取 project/deployment 配置
//   - 不持久化 desired 清单，持久化由 api.ManagedStore 负责
package collector

import "github.com/xsxdot/super-dev/agent/model"

// DesiredCollector 表示一个期望运行的 managed collector。
type DesiredCollector struct {
	ID        string
	Name      string
	Type      model.LogSourceType
	ExtraArgs []string
}

// ReconcileFailure 表示某个期望 collector 未能启动。
type ReconcileFailure struct {
	ID    string
	Name  string
	Type  model.LogSourceType
	Error string
}

// ReconcileResult 表示 collector.Manager 应用期望状态后的变更。
type ReconcileResult struct {
	Started []model.Collector
	Stopped []string
	Failed  []ReconcileFailure
}

// Reconcile 将 managed collector 调整为 desired。
//
// 参数：
//   - desired: 当前 managed deployments 需要运行的 collector 集合
//
// 返回：
//   - 本次启动、停止和失败的 collector 信息
//
// 注意：
//   - 只停止上一次由 Reconcile 接管、但本次不再期望的 collector。
//   - 通过 Start/StartForTest 手动启动的 collector 不会被本方法停止。
func (m *Manager) Reconcile(desired []DesiredCollector) ReconcileResult {
	result := ReconcileResult{
		Started: []model.Collector{},
		Stopped: []string{},
		Failed:  []ReconcileFailure{},
	}
	desiredIDs := map[string]struct{}{}

	for _, item := range desired {
		id := desiredCollectorID(item)
		desiredIDs[id] = struct{}{}
		alreadyRunning := false
		if _, ok := m.Get(id); ok {
			alreadyRunning = true
		}
		startedID, err := m.startWithOptionsAs(id, item.Name, item.Type, item.ExtraArgs)
		if err != nil {
			result.Failed = append(result.Failed, ReconcileFailure{ID: id, Name: item.Name, Type: item.Type, Error: err.Error()})
			continue
		}
		m.mu.Lock()
		m.managed[startedID] = struct{}{}
		m.mu.Unlock()
		if !alreadyRunning {
			if col, ok := m.Get(startedID); ok {
				result.Started = append(result.Started, col)
			}
		}
	}

	m.mu.Lock()
	var toStop []string
	for id := range m.managed {
		if _, ok := desiredIDs[id]; !ok {
			toStop = append(toStop, id)
		}
	}
	m.mu.Unlock()

	for _, id := range toStop {
		_ = m.Stop(id)
		m.mu.Lock()
		delete(m.managed, id)
		m.mu.Unlock()
		result.Stopped = append(result.Stopped, id)
	}
	return result
}

func desiredCollectorID(item DesiredCollector) string {
	if item.ID != "" {
		return item.ID
	}
	return CollectorID(item.Name, item.Type)
}
