// Package api 的 Host 删除应用操作。
//
// 职责：
//   - 在 Host 级生命周期互斥门内检查 Agent 配置引用
//   - 检查该 Host 是否仍是某些项目的归属（project_home 守卫）
//   - 仅在 Agent 已卸载或 Detach、且不再是任何项目归属后删除 Host 配置
//   - 返回供 HTTP 入口映射的稳定冲突码
//
// 边界：
//   - 不级联执行 Agent 卸载或 Detach
//   - 不执行归属迁移，只读 projectHomeStore 做拦截判断
//   - 不解析 HTTP 请求或写 HTTP 响应
package api

import (
	"context"
	"fmt"

	"github.com/xsxdot/gokit/logger"
)

const hostDeleteCodeAgentConfigured = "agent_configured"

// hostDeleteCodeProjectHome 标记该 Host 仍是若干项目的归属，不能直接删除——
// 删除会留下指向已消失主机的悬空归属记录（projecthome.Store 不校验 hostID
// 是否真实存在，职责边界见该包文件头），必须引导用户先在项目概览把归属
// 迁回本机或其他主机。
const hostDeleteCodeProjectHome = "project_home"

type hostDeleteError struct {
	Code     string
	Conflict *hostOperationConflict
	// ProjectNames 仅在 Code == hostDeleteCodeProjectHome 时有值，供 HTTP 层
	// 拼进 409 响应的 detail，让用户直接看到"是哪些项目"而不是一串 ID。
	ProjectNames []string
	Err          error
}

// Error 返回 Host 删除的稳定错误码及底层原因。
func (e *hostDeleteError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Err.Error()
}

// removeHostSafely 在 Agent 生命周期 gate 内删除不再被 Agent 配置引用的 Host。
//
// 参数：
//   - ctx: 请求上下文，传给 tunnel 失效协调
//   - hostID: 待删除的 Host ID
//
// 返回：
//   - agent_configured 表示必须先卸载或 Detach Agent
//   - project_home 表示该 Host 仍是若干项目的归属，须先在项目概览迁回
//   - operation_in_progress 表示同一 Host 正在执行其他 Agent 生命周期操作
//   - 其他错误表示配置读取或写入失败
//
// 注意：该操作绝不级联卸载或 Detach Agent、也绝不级联迁回项目归属；最终
// 删除走 remoteNodeMutations，保证旧 tunnel 运行态随配置删除失效并完成审计。
func (a *App) removeHostSafely(ctx context.Context, hostID string) *hostDeleteError {
	log := logger.GetLogger().WithEntryName("HostLifecycle")
	fields := map[string]any{"host_id": hostID, "operation": "delete_host"}
	log.WithFields(fields).Info("开始删除 Host")

	// 与 Agent 配置写操作共享同一 Host gate，避免检查后并发创建 Agent 形成新孤儿配置。
	release, conflict := a.beginAgentLifecycleOperation(hostID, "delete_host")
	if conflict != nil {
		log.WithErr(conflict).WithFields(fields).Error("Host 删除被同 Host 生命周期操作拒绝")
		return &hostDeleteError{Code: "operation_in_progress", Conflict: conflict, Err: conflict}
	}
	defer release()

	if _, configured, err := a.agentStore.AgentByHostID(hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("删除 Host 前读取 Agent 配置失败")
		return &hostDeleteError{Err: err}
	} else if configured {
		err := fmt.Errorf("agent configuration still references Host")
		log.WithErr(err).WithFields(fields).Info("Host 仍有 Agent 配置，拒绝删除旁路")
		return &hostDeleteError{Code: hostDeleteCodeAgentConfigured, Err: err}
	}

	// project_home 守卫：与上面的 agent_configured 并列，同一互斥门内检查。
	// 删除一个仍被项目当作归属的 Host 会让 projecthome.Store 里留下一条指向
	// 已消失主机的悬空记录（该 store 自身不校验 hostID 存在性，职责边界见
	// projecthome 包文件头），后续该项目的所有 dev 运行/配置/日志请求都会
	// 转发到一个再也拨不通的地址——必须在这里提前拦截，而不是等运行期才报错。
	if names := a.projectNamesHomedOn(hostID); len(names) > 0 {
		log.WithFields(fields).WithField("project_count", len(names)).
			Info("Host 仍是若干项目的归属，拒绝删除")
		err := fmt.Errorf("host is still the home of %d project(s)", len(names))
		return &hostDeleteError{Code: hostDeleteCodeProjectHome, Err: err, ProjectNames: names}
	}

	if err := a.remoteNodeMutations.RemoveHost(ctx, hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("删除 Host 配置失败")
		return &hostDeleteError{Err: err}
	}
	log.WithFields(fields).Info("Host 配置删除完成")
	return nil
}

// projectNamesHomedOn 把归属于 hostID 的项目 ID 列表解析为项目名，供
// project_home 删除守卫的 detail 与「开发机模式关闭」非阻断提示（见
// handler_hosts.go updateHost）复用同一份查找逻辑，不重复实现两遍。
//
// 参数：
//   - hostID: 待反查的主机 ID
//
// 返回：
//   - 归属该主机的项目名清单，顺序沿用 ProjectsHomedOn 的字典序；某个项目
//     ID 在当前项目列表里查不到（例如项目已被移除但归属记录尚未清理这一
//     异常态）时，退化为直接使用该 ID 占位而不是静默跳过——调用方（删除
//     守卫 / 前端提示）需要如实看到"确实还有 N 条归属"，即便其中某条已经
//     解析不出真实名字；返回 nil 与返回空 slice 对调用方语义等价，均表示
//     "当前没有任何项目归属于该 Host"。
func (a *App) projectNamesHomedOn(hostID string) []string {
	if a.projectHomeStore == nil {
		return nil
	}
	ids := a.projectHomeStore.ProjectsHomedOn(hostID)
	if len(ids) == 0 {
		return nil
	}
	// findProject 要求调用方自行持有锁（见 server.go 注释），这里用一次
	// RLock 覆盖整批查找，避免逐条加解锁。
	a.mu.RLock()
	defer a.mu.RUnlock()
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if p, ok := a.findProject(id); ok && p.Name != "" {
			names = append(names, p.Name)
		} else {
			names = append(names, id)
		}
	}
	return names
}
