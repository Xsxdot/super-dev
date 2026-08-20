// approval_token_origin.go 记录「本机代理取回的审批 token 由哪台 agent 签发」。
//
// 职责：
//   - 代理 GET /api/operation-approvals/{id} 取回外来审批的 token 时登记来源
//   - 供归属路由判断某个 token 是否可以被转发给某台 agent
//
// 边界：
//   - 只记 token 与来源主机的对应关系，**不解析、不校验、不存储 token 的语义**
//     （对本机而言 token 是不透明字符串）
//   - 只登记**外来**审批的 token。本机自己签发的 token 一律不登记，因此在
//     放行判据里天然落进「不放行」——这正是原红线要防的凭据外流
//   - 内存态，进程重启即清空：token 本身寿命很短，重启后桌面端会重新取
package api

import (
	"strings"
	"sync"
	"time"
)

const approvalTokenOriginFallbackTTL = 5 * time.Minute

type approvalTokenOrigin struct {
	mu      sync.Mutex
	entries map[string]approvalTokenOriginEntry
	now     func() time.Time // 测试注入
}

type approvalTokenOriginEntry struct {
	HostID    string
	ExpiresAt time.Time
}

// Remember 登记一张由 hostID 签发的 token。
// expiresAt 取源节点返回的 token 过期时间；为零值时用 approvalTokenOriginFallbackTTL。
func (o *approvalTokenOrigin) Remember(token, hostID string, expiresAt time.Time) {
	token = strings.TrimSpace(token)
	hostID = strings.TrimSpace(hostID)
	if token == "" || hostID == "" {
		return
	}
	now := o.clockNow()
	if expiresAt.IsZero() {
		expiresAt = now.Add(approvalTokenOriginFallbackTTL)
	}
	if !expiresAt.After(now) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entries == nil {
		o.entries = make(map[string]approvalTokenOriginEntry)
	}
	o.entries[token] = approvalTokenOriginEntry{HostID: hostID, ExpiresAt: expiresAt}
}

// OriginOf 返回签发该 token 的主机 ID；未登记或已过期返回空串。
// 顺带惰性清理过期条目——token 寿命短，不需要独立的清理 goroutine。
func (o *approvalTokenOrigin) OriginOf(token string) string {
	now := o.clockNow()
	token = strings.TrimSpace(token)
	o.mu.Lock()
	defer o.mu.Unlock()
	for key, entry := range o.entries {
		if !entry.ExpiresAt.After(now) {
			delete(o.entries, key)
		}
	}
	if token == "" {
		return ""
	}
	return o.entries[token].HostID
}

func (o *approvalTokenOrigin) clockNow() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}
