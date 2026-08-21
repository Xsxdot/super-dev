// provision_approval_gate.go —— 临时库供给的断连审批适配。
//
// 职责：
//   - 把 dbprovision.Plan 中声明的开发库断连副作用转换为 operation 审批计划
//   - 复用现有审批文件存储、一次性 token 与项目豁免窗口
//
// 边界：
//   - 不执行 PostgreSQL 断连或临时资源供给
//   - 不持有明文 DSN、密码或数据库连接
//   - 只负责审批门禁；HTTP 状态码与请求参数编解码由 handler 层处理
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/operation"
)

type provisionApprovalTokenKey struct{}

// WithProvisionApprovalToken 将 HTTP 请求携带的一次性审批 token 放入供给上下文。
//
// 参数：
//   - ctx: 当前供给请求上下文
//   - token: X-SuperDev-Approval-Token 的值
//
// 返回：
//   - 可传给 Manager.Acquire 的派生上下文
//
// 注意：token 只在内存上下文中短暂传递，不写入日志或持久化数据。
func WithProvisionApprovalToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, provisionApprovalTokenKey{}, strings.TrimSpace(token))
}

// ApprovalRequiredError 表示临时库断连动作已创建审批请求、尚未执行。
//
// 调用方可通过 errors.As 取出 Approval，并把它序列化成现有
// approval_required 响应；审批 token 由后续请求经 WithProvisionApprovalToken 传回。
type ApprovalRequiredError struct {
	Approval operation.Approval
}

// Error 返回不包含连接信息的审批等待摘要。
func (e ApprovalRequiredError) Error() string {
	if e.Approval.ID == "" {
		return "approval_required"
	}
	return fmt.Sprintf("approval_required: %s", e.Approval.ID)
}

// ProvisionApprovalGate 把临时库供给计划接到现有 operation 审批体系。
type ProvisionApprovalGate struct {
	settings  *config.SettingsStore
	approvals operation.ApprovalStore
	grace     operation.GraceStore
}

// NewProvisionApprovalGate 创建临时库供给审批门禁。
//
// 参数：
//   - settings: 提供 test_database_terminate_conns 开关的设置存储
//   - approvals: 现有 operation 审批存储
//   - grace: 可选的项目级豁免窗口存储；传入后复用现有豁免语义
//
// 返回：
//   - 可注入 dbprovision.Manager 的审批门禁
//
// 注意：settings 与 approvals 不应为 nil；nil 只适合不会触发副作用的测试装配。
func NewProvisionApprovalGate(settings *config.SettingsStore, approvals operation.ApprovalStore, grace ...operation.GraceStore) dbprovision.ApprovalGate {
	gate := &ProvisionApprovalGate{settings: settings, approvals: approvals}
	if len(grace) > 0 {
		gate.grace = grace[0]
	}
	return gate
}

// Authorize 检查计划中的断连副作用，必要时创建并等待 operation 审批。
//
// 参数：
//   - ctx: 请求上下文，可能携带审批 token
//   - projectID: 本次供给所属项目
//   - plans: 已完成只读探测的供给计划
//
// 返回：
//   - nil 表示无副作用、设置免审、豁免窗口命中或 token 已消费
//   - ApprovalRequiredError 表示已创建 pending 审批，调用方应返回 approval_required
//   - 其他 error 表示审批存储或 token 校验失败
//
// 注意：设置读取失败按需要审批处理（fail-closed），不能因为配置暂时不可读而放行断连。
func (g *ProvisionApprovalGate) Authorize(ctx context.Context, projectID string, plans []dbprovision.Plan) error {
	target, count, detail, ok := terminateEffect(plans)
	log := logger.GetLogger().WithEntryName("ProvisionApproval")
	if !ok {
		log.WithField("project_id", projectID).Debug("临时库供给无断连副作用，直接放行")
		return nil
	}

	policyEnabled := true
	if g.settings == nil {
		log.WithField("project_id", projectID).Error("临时库审批设置存储未装配，按需要审批处理")
	} else if settings, err := g.settings.Load(); err != nil {
		log.WithErr(err).WithField("project_id", projectID).Error("读取临时库审批设置失败，按需要审批处理")
	} else {
		policyEnabled = settings.Approval.TestDatabaseTerminateConns
	}

	plan := operation.PlanTestDatabaseTerminate(projectID, target, count, detail)
	if g.grace != nil {
		if _, active, err := g.grace.ActiveGrace(ctx, projectID); err == nil && active {
			log.WithFields(map[string]any{"project_id": projectID, "target": target, "count": count}).
				Info("项目豁免窗口放行临时库断连")
			return nil
		} else if err != nil {
			log.WithErr(err).WithField("project_id", projectID).Error("读取项目审批豁免窗口失败，按需要审批处理")
		}
	}
	if !policyEnabled {
		// 免审等价于用户显式授权了断连，必须留 Info 审计线索。
		log.WithFields(map[string]any{"project_id": projectID, "target": target, "count": count}).
			Info("临时库断连按设置免审放行")
		return nil
	}
	if g.approvals == nil {
		return errors.New("provision approval store is not configured")
	}

	token, _ := ctx.Value(provisionApprovalTokenKey{}).(string)
	if strings.TrimSpace(token) != "" {
		if _, err := g.approvals.ConsumeToken(ctx, token, plan.Fingerprint); err != nil {
			log.WithErr(err).WithField("project_id", projectID).Error("消费临时库断连审批 token 失败")
			return err
		}
		log.WithFields(map[string]any{"project_id": projectID, "target": target, "count": count}).
			Info("临时库断连审批 token 已消费")
		return nil
	}

	approval, err := g.approvals.FindOrCreatePending(ctx, plan, "mcp", "AI")
	if err != nil {
		log.WithErr(err).WithField("project_id", projectID).Error("创建临时库断连审批请求失败")
		return err
	}
	log.WithFields(map[string]any{
		"project_id":  projectID,
		"approval_id": approval.ID,
		"target":      target,
		"count":       count,
	}).Info("已创建临时库断连审批请求")
	return ApprovalRequiredError{Approval: approval}
}

func terminateEffect(plans []dbprovision.Plan) (string, int, string, bool) {
	for _, plan := range plans {
		for _, effect := range plan.SideEffects {
			if effect.Kind == dbprovision.SideEffectTerminateConnections {
				return effect.Target, effect.Count, effect.Detail, true
			}
		}
	}
	return "", 0, "", false
}
