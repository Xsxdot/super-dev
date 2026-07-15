// cleanup.go 按固定 acquisition 逆序释放当前 campaign 拥有的资源。
//
// 职责：
//   - 把 mutation 纳入同一个 intent/acquired/released journal
//   - 在正常错误、取消和超时路径持续执行可安全释放的后续动作
//   - 把失败动作保留为 residual，禁止 active marker 被错误删除
//
// 边界：
//   - 不删除 borrowed Host/Agent/Tunnel，不 replay 旧 bundle journal
//   - pipeline 未终态时不与远端 run 竞争，也不把远端 root 记为 released
package runtimevalidation

import (
	"context"
	"fmt"
	"sync"

	"github.com/xsxdot/gokit/logger"
)

// CleanupStack 管理本次进程已 acquired 的 campaign-owned actions。
type CleanupStack struct {
	mu      sync.Mutex
	journal *CleanupJournal
	actions []CleanupAction
	closed  bool
	facts   CleanupResult
}

// NewCleanupStack 创建一个绑定当前 run journal 的 cleanup stack。
func NewCleanupStack(journal *CleanupJournal) *CleanupStack {
	return &CleanupStack{journal: journal}
}

// Track 在 mutation 已成功后登记一个需要逆序释放的 action。
//
// 参数：
//   - action: 具备稳定 kind/id 和幂等 Release 的 campaign-owned 资源
//
// 返回：
//   - journal intent/acquired 或重复/已关闭错误
//
// 注意：生产 mutation 应优先使用 Acquire，以保证 intent 在外部调用前 fsync。
func (s *CleanupStack) Track(action CleanupAction) error {
	if action == nil {
		return fmt.Errorf("cleanup action is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("cleanup stack no longer accepts resources")
	}
	for _, existing := range s.actions {
		if existing.Kind() == action.Kind() && existing.ID() == action.ID() {
			return fmt.Errorf("cleanup action %s/%s is duplicated", action.Kind(), action.ID())
		}
	}
	if s.journal != nil {
		if err := s.journal.Intent(action.Kind(), action.ID(), s.journal.campaign, nil); err != nil {
			return err
		}
		if err := s.journal.Acquired(action.Kind(), action.ID(), s.journal.campaign); err != nil {
			return err
		}
	}
	s.actions = append(s.actions, action)
	logger.GetLogger().WithEntryName("RuntimeValidationCleanup").WithFields(map[string]any{"resource_kind": action.Kind(), "resource_id": action.ID()}).Info("campaign-owned cleanup action 已登记")
	return nil
}

// Acquire 在外部 mutation 前写 intent，成功后写 acquired 并登记 cleanup action。
//
// 参数：
//   - kind/id: 稳定资源身份
//   - preimage: cleanup 必需的非秘密旧状态
//   - mutate: 执行一次外部 mutation 并返回对应 cleanup action
//
// 返回：
//   - mutation 产生的 action
//   - journal、mutation 或资源身份错误
//
// 注意：mutation 失败保留 intent 供报告定位，但不会伪造 acquired。
func (s *CleanupStack) Acquire(kind, id string, preimage map[string]any, mutate func() (CleanupAction, error)) (CleanupAction, error) {
	if s.journal == nil {
		return nil, fmt.Errorf("cleanup journal is required for mutation")
	}
	if err := s.journal.Intent(kind, id, s.journal.campaign, preimage); err != nil {
		return nil, err
	}
	logger.GetLogger().WithEntryName("RuntimeValidationCleanup").WithFields(map[string]any{"resource_kind": kind, "resource_id": id}).Info("开始 campaign-owned mutation")
	action, err := mutate()
	if err != nil {
		logger.GetLogger().WithEntryName("RuntimeValidationCleanup").WithErr(err).WithFields(map[string]any{"resource_kind": kind, "resource_id": id}).Error("campaign-owned mutation 失败")
		return nil, err
	}
	if action == nil || action.Kind() != kind || action.ID() != id {
		return nil, fmt.Errorf("mutation returned mismatched cleanup action for %s/%s", kind, id)
	}
	if err := s.journal.Acquired(kind, id, s.journal.campaign); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("cleanup stack closed while acquiring %s/%s", kind, id)
	}
	s.actions = append(s.actions, action)
	return action, nil
}

// SetTerminalFacts 记录 pipeline、远端 root、borrowed topology 与 marker 的 cleanup gates。
//
// 参数：
//   - pipelineTerminal: pipeline run 已到产品终态
//   - remoteRootAbsent: 非 self 远端 campaign root 已确认不存在
//   - borrowedTopologyStable: foundation/Host/Agent/Tunnel digest 未漂移
//   - activeMarkerRemoved: marker 只在前三项及 residual scan 通过后删除
func (s *CleanupStack) SetTerminalFacts(pipelineTerminal, remoteRootAbsent, borrowedTopologyStable, activeMarkerRemoved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts.PipelineTerminal = pipelineTerminal
	s.facts.RemoteRootAbsent = remoteRootAbsent
	s.facts.BorrowedTopologyStable = borrowedTopologyStable
	s.facts.ActiveMarkerRemoved = activeMarkerRemoved
}

// ReleaseTracked 在正常主路径提前释放一个已登记资源并从逆序 stack 移除。
//
// 参数：
//   - ctx: release deadline
//   - kind/id: 已 acquired 的稳定资源身份
//
// 返回：
//   - action 或 released journal 失败错误
//
// 注意：失败时 action 保留在 stack，最终 Cleanup 会再次按幂等合同尝试。
func (s *CleanupStack) ReleaseTracked(ctx context.Context, kind, id string) error {
	s.mu.Lock()
	index := -1
	var action CleanupAction
	for candidate, current := range s.actions {
		if current.Kind() == kind && current.ID() == id {
			index = candidate
			action = current
			break
		}
	}
	s.mu.Unlock()
	if index < 0 {
		return fmt.Errorf("cleanup action %s/%s is not tracked", kind, id)
	}
	if err := action.Release(ctx); err != nil {
		return err
	}
	if s.journal != nil {
		if err := s.journal.Released(kind, id, s.journal.campaign); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.actions = append(s.actions[:index], s.actions[index+1:]...)
	s.mu.Unlock()
	logger.GetLogger().WithEntryName("RuntimeValidationCleanup").WithFields(map[string]any{"resource_kind": kind, "resource_id": id}).Info("campaign-owned 资源已在正常主路径释放")
	return nil
}

// Cleanup 停止接收新资源并按 acquisition 逆序执行全部安全 release。
//
// 参数：
//   - ctx: 当前 cleanup deadline；每个 action 必须自行尊重取消
//
// 返回：
//   - journal 完整性、终态 facts 与 residual 列表
//
// 注意：单个 action 失败不会跳过后续本地安全清理。
func (s *CleanupStack) Cleanup(ctx context.Context) CleanupResult {
	s.mu.Lock()
	s.closed = true
	actions := append([]CleanupAction{}, s.actions...)
	result := s.facts
	s.mu.Unlock()
	log := logger.GetLogger().WithEntryName("RuntimeValidationCleanup").WithField("action_count", len(actions))
	log.Info("开始逆序清理 runtime validation campaign 资源")
	for index := len(actions) - 1; index >= 0; index-- {
		action := actions[index]
		fields := map[string]any{"resource_kind": action.Kind(), "resource_id": action.ID(), "reverse_index": len(actions) - index}
		log.WithFields(fields).Info("开始释放 campaign-owned 资源")
		if err := action.Release(ctx); err != nil {
			result.Residuals = append(result.Residuals, Residual{Kind: action.Kind(), ID: action.ID(), Detail: err.Error()})
			log.WithErr(err).WithFields(fields).Error("campaign-owned 资源释放失败，记录 residual")
			continue
		}
		if s.journal != nil {
			if err := s.journal.Released(action.Kind(), action.ID(), s.journal.campaign); err != nil {
				result.Residuals = append(result.Residuals, Residual{Kind: action.Kind(), ID: action.ID(), Detail: "write released journal: " + err.Error()})
				log.WithErr(err).WithFields(fields).Error("资源已释放但 journal 写入失败")
				continue
			}
		}
		log.WithFields(fields).Info("campaign-owned 资源已释放")
	}
	if s.journal == nil {
		result.JournalComplete = len(result.Residuals) == 0
	} else {
		result.JournalComplete = s.journal.Snapshot().Complete && len(result.Residuals) == 0
	}
	if len(result.Residuals) > 0 {
		log.WithField("residual_count", len(result.Residuals)).Error("runtime validation campaign cleanup 留有 residual")
		return result
	}
	log.Info("runtime validation campaign cleanup 动作全部完成")
	return result
}
