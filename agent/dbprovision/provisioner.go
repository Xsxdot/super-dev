// provisioner.go —— 资源供给插件接口与 kind→实现的全局注册表。
//
// 职责：定义新增资源类型时唯一需要实现的契约，并提供进程级注册与查找。
// 边界：不含任何具体资源类型的知识；注册表只做映射，不做生命周期管理。
package dbprovision

import (
	"context"
	"sync"

	"github.com/xsxdot/gokit/logger"
)

// Provisioner 是一种资源类型的供给实现。
//
// 实现约定（违反任一条都会在并发或崩溃恢复时出错）：
//   - 所有方法必须可被并发调用
//   - Plan 只做只读探测，不得产生副作用
//   - Reclaim 必须幂等：资源已不存在时返回 nil 而不是错误
//   - Provision 失败时必须自行回滚已创建的中间产物，不留半成品
type Provisioner interface {
	// Kind 返回该实现负责的资源类型标识。
	Kind() string
	// Probe 探测管理连接的连通性与所需能力，供登记时立即反馈。
	Probe(ctx context.Context, ds DataSource) (ProbeResult, error)
	// Plan 计算本次供给将要做什么，含最终资源标识与副作用声明。
	Plan(ctx context.Context, ds DataSource, req PlanRequest) (Plan, error)
	// Provision 按 Plan 真正创建资源，返回含明文 DSN 的 Resource。
	Provision(ctx context.Context, ds DataSource, plan Plan) (Resource, error)
	// Reclaim 回收一个资源，必须幂等。
	Reclaim(ctx context.Context, ds DataSource, res Resource) error
	// Reconcile 对比实例实况与已知登记，返回登记表之外的残留资源。
	//
	// 注意：实现只允许报告自己能确证是本供给层产物的残留（如带前缀的库）。
	// 无法确证归属的资源必须放过——误回收用户数据是不可逆的。
	Reconcile(ctx context.Context, ds DataSource, known []Resource) ([]Orphan, error)
}

var (
	provisionerMu sync.RWMutex
	provisioners  = map[string]Provisioner{}
)

// RegisterProvisioner 注册一个资源类型的供给实现。
//
// 注意：同 kind 重复注册会覆盖旧实现——这是为了让测试能替换实现，
// 生产代码里每个 kind 只应在各自的 init 或装配函数中注册一次。
func RegisterProvisioner(p Provisioner) {
	if p == nil {
		return
	}
	provisionerMu.Lock()
	defer provisionerMu.Unlock()
	if _, exists := provisioners[p.Kind()]; exists {
		logger.GetLogger().WithEntryName("DBProvision").WithField("kind", p.Kind()).Warn("重复注册资源供给实现，旧实现被覆盖")
	}
	provisioners[p.Kind()] = p
}

// LookupProvisioner 按 kind 查找供给实现。
//
// 返回：
//   - 实现与 true；未注册时返回 nil 与 false
func LookupProvisioner(kind string) (Provisioner, bool) {
	provisionerMu.RLock()
	defer provisionerMu.RUnlock()
	p, ok := provisioners[kind]
	return p, ok
}
