// Package debugcredential 管理只存在于 Agent 进程内的调试凭据 lease。
//
// 职责：
//   - 按 project/service scope 和 owner 保存有 TTL 的明文调试凭据
//   - 在创建、读取和删除时回收过期 lease
//   - 返回不含明文的 lease metadata，供 HTTP 层安全响应
//
// 边界：
//   - 不写配置、数据库、文件、日志证据或系统钥匙串
//   - 不负责 HTTP 鉴权；调用者必须通过 Agent 的既有安全边界
//   - 不把凭据值、hash 或 token 写入日志
package debugcredential

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// DefaultTTL 是调用者没有指定 TTL 时的 lease 生命周期。
	DefaultTTL = 30 * time.Minute
	// MaxTTL 限制一次进程内授权的最大生命周期，避免把 lease 变成长驻 secret store。
	MaxTTL                 = 2 * time.Hour
	maxCredentialsPerLease = 16
)

var (
	// ErrInvalidLease 表示 lease scope、owner、TTL 或 credential 合同不合法。
	ErrInvalidLease = errors.New("invalid debug credential lease")
	// ErrLeaseConflict 表示同一 owner 和 scope 已有活动 lease。
	ErrLeaseConflict = errors.New("active debug credential lease already exists for owner and scope")
	// ErrLeaseNotFound 表示 lease 不存在、已过期或 owner 不匹配。
	ErrLeaseNotFound = errors.New("debug credential lease not found")
)

// CreateRequest 描述一次显式的进程内调试凭据授权。
type CreateRequest struct {
	ProjectID   string                  `json:"project_id"`
	ServiceID   string                  `json:"service_id,omitempty"`
	Owner       string                  `json:"owner"`
	TTLSeconds  int                     `json:"ttl_seconds,omitempty"`
	Credentials []model.DebugCredential `json:"credentials"`
}

// Metadata 是可安全返回和记录的 lease 身份，不包含 credential value。
type Metadata struct {
	ID           string                      `json:"id"`
	ProjectID    string                      `json:"project_id"`
	ServiceID    string                      `json:"service_id,omitempty"`
	Owner        string                      `json:"owner"`
	ExpiresAtUTC time.Time                   `json:"expires_at_utc"`
	Count        int                         `json:"count"`
	Hints        []model.DebugCredentialHint `json:"credential_hints"`
}

// ActiveCredentials 保存一个查询 scope 当前活动的 project/service lease 凭据。
//
// 注意：该类型只在 Agent 进程内传递；调用方不得序列化到普通快照或证据。
type ActiveCredentials struct {
	Project []model.DebugCredential
	Service []model.DebugCredential
}

// Options 允许测试注入时钟；生产默认使用 time.Now。
type Options struct {
	Now func() time.Time
}

type lease struct {
	metadata    Metadata
	credentials []model.DebugCredential
	createdAt   time.Time
}

// Store 是并发安全、非持久化的 lease module。
type Store struct {
	mu     sync.Mutex
	now    func() time.Time
	leases map[string]*lease
}

// NewStore 创建一个空的进程内 lease store。
//
// 参数：
//   - options: 可选时钟；nil 时使用 UTC 当前时间
//
// 返回：
//   - 不读取任何持久状态的空 Store；因此 Agent 重启天然清空全部 lease
func NewStore(options Options) *Store {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{now: now, leases: map[string]*lease{}}
}

// Create 创建一个有界 TTL 的进程内 lease，并只返回脱敏 metadata。
func (s *Store) Create(req CreateRequest) (Metadata, error) {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.Owner = strings.TrimSpace(req.Owner)
	for i := range req.Credentials {
		req.Credentials[i].Name = strings.TrimSpace(req.Credentials[i].Name)
	}
	if err := validateCreateRequest(req); err != nil {
		logger.GetLogger().WithEntryName("DebugCredentialLease").WithErr(err).Error("拒绝创建不合法的进程内调试凭据 lease")
		return Metadata{}, err
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.purgeExpiredLocked(now)
	for _, existing := range s.leases {
		if existing.metadata.ProjectID == req.ProjectID && existing.metadata.ServiceID == req.ServiceID && existing.metadata.Owner == req.Owner {
			logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(safeFields(existing.metadata)).Error("同 owner 和 scope 的调试凭据 lease 已存在")
			return Metadata{}, ErrLeaseConflict
		}
	}

	source := "ephemeral_project"
	if req.ServiceID != "" {
		source = "ephemeral_service"
	}
	hints := make([]model.DebugCredentialHint, 0, len(req.Credentials))
	credentials := make([]model.DebugCredential, len(req.Credentials))
	copy(credentials, req.Credentials)
	for _, credential := range credentials {
		hints = append(hints, model.DebugCredentialHint{Name: credential.Name, Desc: credential.Desc, Source: source})
	}
	metadata := Metadata{
		ID: uuid.NewString(), ProjectID: req.ProjectID, ServiceID: req.ServiceID, Owner: req.Owner,
		ExpiresAtUTC: now.Add(ttl), Count: len(credentials), Hints: hints,
	}
	s.leases[metadata.ID] = &lease{metadata: metadata, credentials: credentials, createdAt: now}
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(safeFields(metadata)).Info("进程内调试凭据 lease 已创建")
	return cloneMetadata(metadata), nil
}

// Active 返回指定 project/service 查询可见的活动 lease 凭据。
//
// 参数：
//   - projectID: 必填项目稳定 ID
//   - serviceID: 可选服务稳定 ID；为空时不返回服务级 lease
//
// 返回：
//   - project 与 service 两层凭据，按创建时间稳定排序
//
// 注意：
//   - 读取前会回收过期项；返回值是副本，调用方可在使用后丢弃
func (s *Store) Active(projectID, serviceID string) ActiveCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	expired := s.purgeExpiredLocked(now)
	ordered := make([]*lease, 0, len(s.leases))
	for _, candidate := range s.leases {
		if candidate.metadata.ProjectID != projectID {
			continue
		}
		if candidate.metadata.ServiceID != "" && candidate.metadata.ServiceID != serviceID {
			continue
		}
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].createdAt.Equal(ordered[j].createdAt) {
			return ordered[i].metadata.ID < ordered[j].metadata.ID
		}
		return ordered[i].createdAt.Before(ordered[j].createdAt)
	})
	var active ActiveCredentials
	for _, candidate := range ordered {
		copyOfCredentials := append([]model.DebugCredential(nil), candidate.credentials...)
		if candidate.metadata.ServiceID == "" {
			active.Project = append(active.Project, copyOfCredentials...)
		} else {
			active.Service = append(active.Service, copyOfCredentials...)
		}
	}
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(map[string]any{
		"project_id": projectID, "service_id": serviceID,
		"project_count": len(active.Project), "service_count": len(active.Service), "expired_removed": expired,
	}).Info("进程内调试凭据 lease 已读取")
	return active
}

// Delete 按 lease ID 与 owner 精确删除一条活动 lease。
func (s *Store) Delete(id, owner string) (Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.purgeExpiredLocked(now)
	candidate, ok := s.leases[strings.TrimSpace(id)]
	if !ok || candidate.metadata.Owner != strings.TrimSpace(owner) {
		logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(map[string]any{"has_lease_id": strings.TrimSpace(id) != "", "has_owner": strings.TrimSpace(owner) != ""}).Error("进程内调试凭据 lease 精确删除被拒绝")
		return Metadata{}, ErrLeaseNotFound
	}
	metadata := cloneMetadata(candidate.metadata)
	scrubLease(candidate)
	delete(s.leases, strings.TrimSpace(id))
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(safeFields(metadata)).Info("进程内调试凭据 lease 已删除")
	return metadata, nil
}

// RevokeProject 撤销一个项目 scope 下的全部活动 lease。
//
// 参数：
//   - projectID: 已从活动项目视图消失的项目稳定 ID
//
// 返回：
//   - 被撤销的活动 lease 数量；空或非法 ID 返回 0
//
// 注意：
//   - 删除 map 项目前会先清空 credential 结构并解除明文引用
//   - 日志只记录撤销 scope 与数量，不记录 lease、owner 或 credential metadata
func (s *Store) RevokeProject(projectID string) int {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || invalidIdentity(projectID, 256) {
		logger.GetLogger().WithEntryName("DebugCredentialLease").WithField("count", 0).Error("拒绝撤销非法 project scope 的进程内调试凭据 lease")
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(s.now().UTC())
	count := s.revokeScopeLocked(projectID, "", true)
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(map[string]any{
		"project_id": projectID,
		"count":      count,
	}).Info("项目 scope 已撤销进程内调试凭据 lease")
	return count
}

// RevokeService 撤销一个项目内指定服务 scope 的全部活动 lease。
//
// 参数：
//   - projectID: 服务所属项目的稳定 ID
//   - serviceID: 已从活动项目视图消失的服务稳定 ID
//
// 返回：
//   - 被撤销的活动 lease 数量；任一 ID 为空或非法时返回 0
//
// 注意：
//   - 只撤销精确 service scope，不影响同项目的 project lease 或其他服务
//   - 日志只记录撤销 scope 与数量，不记录 lease、owner 或 credential metadata
func (s *Store) RevokeService(projectID, serviceID string) int {
	projectID = strings.TrimSpace(projectID)
	serviceID = strings.TrimSpace(serviceID)
	if projectID == "" || serviceID == "" || invalidIdentity(projectID, 256) || invalidIdentity(serviceID, 256) {
		logger.GetLogger().WithEntryName("DebugCredentialLease").WithField("count", 0).Error("拒绝撤销非法 service scope 的进程内调试凭据 lease")
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(s.now().UTC())
	count := s.revokeScopeLocked(projectID, serviceID, false)
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithFields(map[string]any{
		"project_id": projectID,
		"service_id": serviceID,
		"count":      count,
	}).Info("服务 scope 已撤销进程内调试凭据 lease")
	return count
}

// Clear 立即解除 Store 对全部 lease 明文的引用，供 Agent shutdown 使用。
//
// 返回：
//   - 被清除的 lease 数量
func (s *Store) Clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.leases)
	for id, candidate := range s.leases {
		scrubLease(candidate)
		delete(s.leases, id)
	}
	logger.GetLogger().WithEntryName("DebugCredentialLease").WithField("count", count).Info("Agent shutdown 已清空进程内调试凭据 lease")
	return count
}

func validateCreateRequest(req CreateRequest) error {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.Owner) == "" {
		return fmt.Errorf("%w: project_id and owner are required", ErrInvalidLease)
	}
	if invalidIdentity(req.ProjectID, 256) || invalidIdentity(req.ServiceID, 256) || invalidIdentity(req.Owner, 200) {
		return fmt.Errorf("%w: project_id, service_id, or owner is invalid", ErrInvalidLease)
	}
	// 先比较调用方原始秒数再转换 duration，避免超大 int 乘以 time.Second 后回绕成表面合法的小 TTL。
	if req.TTLSeconds < 0 || req.TTLSeconds > int(MaxTTL/time.Second) {
		return fmt.Errorf("%w: ttl_seconds must be between 0 and %d", ErrInvalidLease, int(MaxTTL/time.Second))
	}
	if len(req.Credentials) == 0 || len(req.Credentials) > maxCredentialsPerLease {
		return fmt.Errorf("%w: credentials count is invalid", ErrInvalidLease)
	}
	seen := map[string]struct{}{}
	for _, credential := range req.Credentials {
		name := strings.TrimSpace(credential.Name)
		if name == "" || len(name) > 128 || strings.TrimSpace(credential.Value) == "" || len(credential.Value) > 64*1024 || len(credential.Desc) > 1024 {
			return fmt.Errorf("%w: credential fields are invalid", ErrInvalidLease)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate credential name", ErrInvalidLease)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func invalidIdentity(value string, maxLength int) bool {
	return len(value) > maxLength || strings.IndexFunc(value, unicode.IsControl) >= 0
}

func (s *Store) purgeExpiredLocked(now time.Time) int {
	removed := 0
	for id, candidate := range s.leases {
		if now.Before(candidate.metadata.ExpiresAtUTC) {
			continue
		}
		// Go string 无法保证原地清零；这里至少立即解除 Store 对明文的所有引用，TTL 后不再可读。
		scrubLease(candidate)
		delete(s.leases, id)
		removed++
	}
	if removed > 0 {
		logger.GetLogger().WithEntryName("DebugCredentialLease").WithField("count", removed).Info("过期进程内调试凭据 lease 已回收")
	}
	return removed
}

func (s *Store) revokeScopeLocked(projectID, serviceID string, wholeProject bool) int {
	removed := 0
	for id, candidate := range s.leases {
		if candidate.metadata.ProjectID != projectID {
			continue
		}
		if !wholeProject && candidate.metadata.ServiceID != serviceID {
			continue
		}
		// 先 scrub 再删除，确保任何仍指向 lease 对象的临时引用也不再持有 credential 结构。
		scrubLease(candidate)
		delete(s.leases, id)
		removed++
	}
	return removed
}

func scrubLease(candidate *lease) {
	for i := range candidate.credentials {
		candidate.credentials[i] = model.DebugCredential{}
	}
	candidate.credentials = nil
}

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Hints = append([]model.DebugCredentialHint(nil), metadata.Hints...)
	return metadata
}

func safeFields(metadata Metadata) map[string]any {
	return map[string]any{
		"lease_id": metadata.ID, "project_id": metadata.ProjectID, "service_id": metadata.ServiceID,
		"owner": metadata.Owner, "count": metadata.Count, "expires_at_utc": metadata.ExpiresAtUTC,
	}
}
