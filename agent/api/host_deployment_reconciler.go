// host_deployment_reconciler.go 将桌面端 project 配置声明式推送到远端 agent。
//
// 职责：
//   - 为单个 host 计算 []model.ManagedDeployment
//   - 经 NodeTransport PUT 完整期望清单
//   - 响应隧道 connected 事件并定期 reconcile
//
// 边界：
//   - 不管理传输生命周期
//   - 不执行远端 collector 或 runtime 采样
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

const defaultManagedDeploymentReconcileInterval = 30 * time.Second

// HostDeploymentReconciler 将桌面端配置推送到远端 host agent。
type HostDeploymentReconciler struct {
	app       *App
	transport nodetransport.NodeTransport
	interval  time.Duration
	mu        sync.Mutex
	hostLocks map[string]*sync.Mutex
}

// NewHostDeploymentReconciler 创建 HostDeploymentReconciler。
//
// 参数：
//   - app: 桌面端 App，作为 projects 的单一事实来源
//   - transport: 按 hostID 访问远端 agent 的节点传输
//   - interval: 周期对账间隔，0 时使用默认 30 秒
//
// 返回：
//   - 可用于手动或后台运行 reconcile 的推送器
func NewHostDeploymentReconciler(app *App, transport nodetransport.NodeTransport, interval time.Duration) *HostDeploymentReconciler {
	if interval == 0 {
		interval = defaultManagedDeploymentReconcileInterval
	}
	return &HostDeploymentReconciler{
		app:       app,
		transport: transport,
		interval:  interval,
		hostLocks: map[string]*sync.Mutex{},
	}
}

// DesiredForHost 计算某 host 的完整 managed deployment 期望清单。
//
// 参数：
//   - hostID: 目标远端主机 ID
//
// 返回：
//   - 该 host 应运行的 remote deployment 清单，已改写为远端本机视角
func (r *HostDeploymentReconciler) DesiredForHost(hostID string) []model.ManagedDeployment {
	r.app.mu.RLock()
	projects := make([]model.Project, len(r.app.projects))
	copy(projects, r.app.projects)
	r.app.mu.RUnlock()
	return desiredDeploymentsForHost(projects, hostID)
}

// Reconcile 将某 host 的完整期望清单推送到远端 agent。
//
// 参数：
//   - ctx: 控制本次 HTTP PUT 的取消和超时
//   - hostID: 目标远端主机 ID
//
// 返回：
//   - host 未连接时返回 nil
//   - URL 构造、序列化、HTTP 请求或远端非 2xx 响应失败时返回错误
func (r *HostDeploymentReconciler) Reconcile(ctx context.Context, hostID string) error {
	hostLock := r.lockForHost(hostID)
	hostLock.Lock()
	defer hostLock.Unlock()

	desired := r.DesiredForHost(hostID)
	body, err := json.Marshal(desired)
	if err != nil {
		return err
	}
	log.Printf("[SuperDev] reconciling managed deployments host=%s desired=%d", hostID, len(desired))
	resp, err := r.transport.Do(ctx, hostID, nodetransport.NodeRequest{
		Method: http.MethodPut,
		Path:   "/api/managed-deployments",
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: bytes.NewReader(body),
	})
	if err != nil {
		if errors.Is(err, nodetransport.ErrHostUnreachable) {
			log.Printf("[SuperDev] skipped managed deployment reconcile host=%s: unreachable", hostID)
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("managed deployment reconcile returned %d for host %s", resp.StatusCode, hostID)
	}
	log.Printf("[SuperDev] managed deployment reconcile completed host=%s desired=%d status=%d", hostID, len(desired), resp.StatusCode)
	return nil
}

func (r *HostDeploymentReconciler) lockForHost(hostID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hostLocks == nil {
		r.hostLocks = map[string]*sync.Mutex{}
	}
	if _, ok := r.hostLocks[hostID]; !ok {
		r.hostLocks[hostID] = &sync.Mutex{}
	}
	return r.hostLocks[hostID]
}

// ReconcileAll 对所有含 remote deployment 的 host 执行 reconcile。
//
// 参数：
//   - ctx: 控制本轮 reconcile 的取消
//
// 返回：
//   - 当前实现始终返回 nil；单 host 失败会记录日志并继续其它 host
func (r *HostDeploymentReconciler) ReconcileAll(ctx context.Context) error {
	for _, hostID := range remoteDeploymentHostIDs(r.snapshotProjects()) {
		if err := r.Reconcile(ctx, hostID); err != nil {
			log.Printf("[SuperDev] managed deployment reconcile failed for host %s: %v", hostID, err)
		}
	}
	return nil
}

// Start 启动隧道事件订阅和周期对账。
//
// 参数：
//   - ctx: App 生命周期上下文，取消后停止后台 goroutine
//
// 注意：
//   - 本方法只启动监听，不会立即执行首次全量 reconcile；调用方负责按需触发。
func (r *HostDeploymentReconciler) Start(ctx context.Context) {
	events := r.app.tunnels.Subscribe("managed-deployment-reconciler")
	ticker := time.NewTicker(r.interval)
	go func() {
		defer ticker.Stop()
		defer r.app.tunnels.Unsubscribe("managed-deployment-reconciler")
		r.run(ctx, events, ticker.C)
	}()
}

func (r *HostDeploymentReconciler) run(ctx context.Context, events <-chan tunnel.Event, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Status == tunnel.StatusConnected {
				if err := r.Reconcile(ctx, ev.HostID); err != nil {
					log.Printf("[SuperDev] managed deployment reconcile failed for host %s: %v", ev.HostID, err)
				}
			}
		case <-ticks:
			_ = r.ReconcileAll(ctx)
		}
	}
}

func (r *HostDeploymentReconciler) snapshotProjects() []model.Project {
	r.app.mu.RLock()
	defer r.app.mu.RUnlock()
	out := make([]model.Project, len(r.app.projects))
	copy(out, r.app.projects)
	return out
}

func desiredDeploymentsForHost(projects []model.Project, hostID string) []model.ManagedDeployment {
	out := []model.ManagedDeployment{}
	seen := map[string]struct{}{}
	for _, project := range projects {
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if dep.Location != model.LocationRemote || !containsString(dep.HostIDs, hostID) {
					continue
				}
				if _, ok := seen[dep.ID]; ok {
					continue
				}
				seen[dep.ID] = struct{}{}
				out = append(out, model.ManagedDeployment{
					DeploymentID: dep.ID,
					ServiceID:    service.ID,
					ServiceName:  service.Name,
					ProjectID:    project.ID,
					EnvName:      dep.EnvName,
					Ports:        dep.Ports,
					Runtime:      dep.Runtime,
					Logs:         deploymentLogsForManaged(dep),
					Location:     model.LocationLocal,
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].DeploymentID < out[j].DeploymentID
	})
	return out
}

func deploymentLogsForManaged(dep model.Deployment) *model.LogConfig {
	if dep.Logs != nil {
		return dep.Logs
	}
	if dep.LogType == "" && dep.LogTarget == "" {
		return nil
	}
	cfg := &model.LogConfig{
		Type:      model.LogKind(dep.LogType),
		ExtraArgs: dep.ExtraArgs,
	}
	// 旧配置只有 LogTarget；投影到新结构时必须按类型放入对应字段，
	// 否则远端 collector 解析 file_tail/command 时会拿不到目标。
	switch cfg.Type {
	case model.LogKindFileTail:
		cfg.Path = dep.LogTarget
	case model.LogKindCommand:
		cfg.Command = dep.LogTarget
	default:
		cfg.Target = dep.LogTarget
	}
	return cfg
}

func remoteDeploymentHostIDs(projects []model.Project) []string {
	seen := map[string]struct{}{}
	for _, project := range projects {
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if dep.Location != model.LocationRemote {
					continue
				}
				for _, hostID := range dep.HostIDs {
					if hostID != "" {
						seen[hostID] = struct{}{}
					}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for hostID := range seen {
		out = append(out, hostID)
	}
	sort.Strings(out)
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
