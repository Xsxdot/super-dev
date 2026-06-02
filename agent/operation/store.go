// 本文件负责 operation 审批与审计事件的本机 JSON 持久化。
//
// 职责：
//   - 保存 pending/approved/rejected/used 等审批状态
//   - 发放并消费一次性 approval token
//   - 保存 operation 安全链路审计事件
//
// 边界：
//   - 不解析项目配置
//   - 不执行被授权的运行态或模板操作
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ApprovalStore 持久化 operation 审批请求和一次性 token 状态。
type ApprovalStore interface {
	FindOrCreatePending(context.Context, Plan, string, string) (Approval, error)
	List(context.Context, ApprovalFilter) ([]Approval, error)
	Get(context.Context, string) (Approval, error)
	Approve(context.Context, string, string, string) (Approval, error)
	Reject(context.Context, string, string, string) (Approval, error)
	IssueToken(context.Context, string) (string, Approval, error)
	ConsumeToken(context.Context, string, string) (Approval, error)
}

// AuditStore 持久化 operation 安全链路事实事件。
type AuditStore interface {
	Append(context.Context, AuditEvent) (AuditEvent, error)
	List(context.Context, AuditFilter) ([]AuditEvent, error)
}

// ApprovalFileStore 将审批状态保存到本机 JSON 文件。
type ApprovalFileStore struct {
	path string
	mu   sync.Mutex
}

// AuditFileStore 将审计事件保存到本机 JSON 文件。
type AuditFileStore struct {
	path  string
	limit int
	mu    sync.Mutex
}

type approvalState struct {
	Approvals []Approval `json:"approvals"`
}

type auditState struct {
	Events []AuditEvent `json:"events"`
}

// NewApprovalFileStore 创建审批 JSON Store。
//
// 参数：
//   - path: 审批状态 JSON 文件完整路径
//
// 返回：
//   - 基于本机文件的审批 Store
func NewApprovalFileStore(path string) *ApprovalFileStore {
	return &ApprovalFileStore{path: path}
}

// NewAuditFileStore 创建审计 JSON Store。
//
// 参数：
//   - path: 审计事件 JSON 文件完整路径
//   - limit: 本机文件最多保留的最新事件数，非正数时使用默认值
//
// 返回：
//   - 基于本机文件的审计 Store
func NewAuditFileStore(path string, limit int) *AuditFileStore {
	if limit <= 0 {
		limit = 5000
	}
	return &AuditFileStore{path: path, limit: limit}
}

// FindOrCreatePending 查询或创建一条待审批请求。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - plan: 预检生成的稳定 operation plan
//   - requestedBy: 请求来源标识
//   - requesterLabel: 请求来源展示名
//
// 返回：
//   - 已存在或新创建的 pending approval
//   - 错误信息
//
// 注意：
//   - 相同 fingerprint 和请求来源的未过期 pending 会复用，避免重复打扰用户
func (s *ApprovalFileStore) FindOrCreatePending(ctx context.Context, plan Plan, requestedBy string, requesterLabel string) (Approval, error) {
	_ = ctx
	requestedBy = strings.TrimSpace(requestedBy)
	requesterLabel = strings.TrimSpace(requesterLabel)

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Approval{}, err
	}

	now := time.Now().UTC()
	changed := expireApprovals(st.Approvals, now)
	for _, approval := range st.Approvals {
		if approval.Status == ApprovalPending &&
			approval.Plan.Fingerprint == plan.Fingerprint &&
			approval.RequestedBy == requestedBy &&
			approval.RequesterLabel == requesterLabel &&
			approval.ExpiresAt.After(now) {
			if changed {
				if err := s.save(st); err != nil {
					return Approval{}, err
				}
			}
			return approval, nil
		}
	}

	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	if plan.ExpiresAt.IsZero() {
		plan.ExpiresAt = now.Add(DefaultPlanTTL)
	}
	approval := Approval{
		ID:             newID("opa"),
		Plan:           plan,
		Status:         ApprovalPending,
		RequestedBy:    requestedBy,
		RequesterLabel: requesterLabel,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(DefaultApprovalTTL),
	}
	st.Approvals = append(st.Approvals, approval)
	if err := s.save(st); err != nil {
		return Approval{}, err
	}
	return approval, nil
}

// List 查询审批请求列表。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - filter: status、project 和数量限制
//
// 返回：
//   - 按更新时间倒序排列的审批请求
//   - 错误信息
func (s *ApprovalFileStore) List(ctx context.Context, filter ApprovalFilter) ([]Approval, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return nil, err
	}

	status := strings.TrimSpace(filter.Status)
	projectID := strings.TrimSpace(filter.ProjectID)
	out := make([]Approval, 0, len(st.Approvals))
	for _, approval := range st.Approvals {
		approval = withEffectiveExpiry(approval, time.Now().UTC())
		if status != "" && approval.Status != status {
			continue
		}
		if projectID != "" && approval.Plan.Target.ProjectID != projectID {
			continue
		}
		out = append(out, approval)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Get 查询单条审批请求。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - id: 审批请求 ID
//
// 返回：
//   - 审批详情
//   - 错误信息
func (s *ApprovalFileStore) Get(ctx context.Context, id string) (Approval, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Approval{}, err
	}
	idx := findApprovalIndex(st.Approvals, strings.TrimSpace(id))
	if idx < 0 {
		return Approval{}, ErrApprovalNotFound
	}
	return withEffectiveExpiry(st.Approvals[idx], time.Now().UTC()), nil
}

// Approve 将 pending 审批请求标记为 approved。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - id: 审批请求 ID
//   - decidedBy: 决策人标识
//   - note: 决策备注
//
// 返回：
//   - 更新后的审批请求
//   - 错误信息
func (s *ApprovalFileStore) Approve(ctx context.Context, id string, decidedBy string, note string) (Approval, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Approval{}, err
	}
	idx := findApprovalIndex(st.Approvals, strings.TrimSpace(id))
	if idx < 0 {
		return Approval{}, ErrApprovalNotFound
	}
	now := time.Now().UTC()
	if err := ensureApprovalDecisionAllowed(st.Approvals[idx], now); err != nil {
		return Approval{}, err
	}
	st.Approvals[idx].Status = ApprovalApproved
	st.Approvals[idx].UpdatedAt = now
	st.Approvals[idx].DecidedAt = &now
	st.Approvals[idx].DecidedBy = strings.TrimSpace(decidedBy)
	st.Approvals[idx].DecisionNote = strings.TrimSpace(note)
	if err := s.save(st); err != nil {
		return Approval{}, err
	}
	return st.Approvals[idx], nil
}

// Reject 将 pending 或 approved 审批请求标记为 rejected。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - id: 审批请求 ID
//   - decidedBy: 决策人标识
//   - note: 决策备注
//
// 返回：
//   - 更新后的审批请求
//   - 错误信息
func (s *ApprovalFileStore) Reject(ctx context.Context, id string, decidedBy string, note string) (Approval, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Approval{}, err
	}
	idx := findApprovalIndex(st.Approvals, strings.TrimSpace(id))
	if idx < 0 {
		return Approval{}, ErrApprovalNotFound
	}
	now := time.Now().UTC()
	if st.Approvals[idx].Status == ApprovalUsed {
		return Approval{}, ErrApprovalTokenConsumed
	}
	if st.Approvals[idx].Status == ApprovalExpired || now.After(st.Approvals[idx].ExpiresAt) {
		st.Approvals[idx].Status = ApprovalExpired
		st.Approvals[idx].UpdatedAt = now
		_ = s.save(st)
		return Approval{}, ErrApprovalExpired
	}

	st.Approvals[idx].Status = ApprovalRejected
	st.Approvals[idx].UpdatedAt = now
	st.Approvals[idx].DecidedAt = &now
	st.Approvals[idx].DecidedBy = strings.TrimSpace(decidedBy)
	st.Approvals[idx].DecisionNote = strings.TrimSpace(note)
	st.Approvals[idx].TokenHash = ""
	st.Approvals[idx].TokenIssuedAt = nil
	st.Approvals[idx].TokenExpiresAt = nil
	if err := s.save(st); err != nil {
		return Approval{}, err
	}
	return st.Approvals[idx], nil
}

// IssueToken 为 approved 审批请求发放一次性 token。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - id: 审批请求 ID
//
// 返回：
//   - 明文 token；已发放过时返回空字符串
//   - 审批详情
//   - 错误信息
//
// 注意：
//   - Store 只保存 token hash，明文 token 只在首次发放时返回
func (s *ApprovalFileStore) IssueToken(ctx context.Context, id string) (string, Approval, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return "", Approval{}, err
	}
	idx := findApprovalIndex(st.Approvals, strings.TrimSpace(id))
	if idx < 0 {
		return "", Approval{}, ErrApprovalNotFound
	}
	now := time.Now().UTC()
	if err := ensureTokenIssueAllowed(st.Approvals[idx], now); err != nil {
		return "", Approval{}, err
	}
	if st.Approvals[idx].TokenHash != "" {
		return "", st.Approvals[idx], nil
	}

	token := newToken()
	tokenIssuedAt := now
	tokenExpiresAt := now.Add(DefaultTokenTTL)
	st.Approvals[idx].TokenHash = tokenHash(token)
	st.Approvals[idx].TokenIssuedAt = &tokenIssuedAt
	st.Approvals[idx].TokenExpiresAt = &tokenExpiresAt
	st.Approvals[idx].UpdatedAt = now
	if err := s.save(st); err != nil {
		return "", Approval{}, err
	}
	return token, st.Approvals[idx], nil
}

// ConsumeToken 校验并消费一次性 approval token。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - token: 用户批准后发放的明文 token
//   - fingerprint: 当前待执行 operation plan 的 fingerprint
//
// 返回：
//   - 被消费的审批请求
//   - 错误信息
//
// 注意：
//   - token 与 fingerprint 同时匹配才允许消费，防止 token 被挪用到其他目标
func (s *ApprovalFileStore) ConsumeToken(ctx context.Context, token string, fingerprint string) (Approval, error) {
	_ = ctx
	token = strings.TrimSpace(token)
	fingerprint = strings.TrimSpace(fingerprint)
	if token == "" || fingerprint == "" {
		return Approval{}, ErrApprovalTokenInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Approval{}, err
	}
	idx := findApprovalByTokenHash(st.Approvals, tokenHash(token))
	if idx < 0 {
		return Approval{}, ErrApprovalTokenInvalid
	}
	now := time.Now().UTC()
	approval := st.Approvals[idx]
	if approval.Status == ApprovalUsed {
		return Approval{}, ErrApprovalTokenConsumed
	}
	if approval.Status == ApprovalRejected {
		return Approval{}, ErrApprovalRejected
	}
	if approval.Status == ApprovalExpired || now.After(approval.ExpiresAt) || tokenExpired(approval, now) {
		st.Approvals[idx].Status = ApprovalExpired
		st.Approvals[idx].UpdatedAt = now
		_ = s.save(st)
		return Approval{}, ErrApprovalExpired
	}
	if approval.Status != ApprovalApproved {
		return Approval{}, ErrApprovalTokenInvalid
	}
	if approval.Plan.Fingerprint != fingerprint {
		return Approval{}, ErrApprovalTokenInvalid
	}

	st.Approvals[idx].Status = ApprovalUsed
	st.Approvals[idx].UpdatedAt = now
	if err := s.save(st); err != nil {
		return Approval{}, err
	}
	return st.Approvals[idx], nil
}

// Append 追加一条 operation 审计事件。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - event: 待保存的审计事件
//
// 返回：
//   - 补齐 ID 和时间后的审计事件
//   - 错误信息
func (s *AuditFileStore) Append(ctx context.Context, event AuditEvent) (AuditEvent, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return AuditEvent{}, err
	}
	now := time.Now().UTC()
	if event.ID == "" {
		event.ID = newID("opaud")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.Kind == "" {
		event.Kind = event.Plan.Kind
	}
	st.Events = append(st.Events, event)
	st.Events = trimAuditEvents(st.Events, s.limit)
	if err := s.save(st); err != nil {
		return AuditEvent{}, err
	}
	return event, nil
}

// List 查询 operation 审计事件。
//
// 参数：
//   - ctx: 上下文，当前文件 Store 不阻塞外部 I/O，仅保留接口一致性
//   - filter: project、kind、approval、时间和数量过滤条件
//
// 返回：
//   - 按创建时间倒序排列的审计事件
//   - 错误信息
func (s *AuditFileStore) List(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return nil, err
	}

	projectID := strings.TrimSpace(filter.ProjectID)
	kind := strings.TrimSpace(filter.Kind)
	approvalID := strings.TrimSpace(filter.ApprovalID)
	out := make([]AuditEvent, 0, len(st.Events))
	for _, event := range st.Events {
		if projectID != "" && event.Plan.Target.ProjectID != projectID {
			continue
		}
		if kind != "" && event.Kind != kind {
			continue
		}
		if approvalID != "" && event.ApprovalID != approvalID {
			continue
		}
		if !filter.Since.IsZero() && event.CreatedAt.Before(filter.Since) {
			continue
		}
		out = append(out, event)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func ensureApprovalDecisionAllowed(approval Approval, now time.Time) error {
	switch approval.Status {
	case ApprovalPending, ApprovalApproved:
	case ApprovalRejected:
		return ErrApprovalRejected
	case ApprovalUsed:
		return ErrApprovalTokenConsumed
	case ApprovalExpired:
		return ErrApprovalExpired
	default:
		return ErrApprovalTokenInvalid
	}
	if now.After(approval.ExpiresAt) {
		return ErrApprovalExpired
	}
	return nil
}

func ensureTokenIssueAllowed(approval Approval, now time.Time) error {
	switch approval.Status {
	case ApprovalApproved:
	case ApprovalRejected:
		return ErrApprovalRejected
	case ApprovalUsed:
		return ErrApprovalTokenConsumed
	case ApprovalExpired:
		return ErrApprovalExpired
	default:
		return ErrApprovalTokenInvalid
	}
	if now.After(approval.ExpiresAt) {
		return ErrApprovalExpired
	}
	if tokenExpired(approval, now) {
		return ErrApprovalExpired
	}
	return nil
}

func expireApprovals(approvals []Approval, now time.Time) bool {
	changed := false
	for i := range approvals {
		if (approvals[i].Status == ApprovalPending || approvals[i].Status == ApprovalApproved) &&
			!approvals[i].ExpiresAt.IsZero() &&
			now.After(approvals[i].ExpiresAt) {
			approvals[i].Status = ApprovalExpired
			approvals[i].UpdatedAt = now
			changed = true
		}
	}
	return changed
}

func withEffectiveExpiry(approval Approval, now time.Time) Approval {
	if (approval.Status == ApprovalPending || approval.Status == ApprovalApproved) &&
		!approval.ExpiresAt.IsZero() &&
		now.After(approval.ExpiresAt) {
		approval.Status = ApprovalExpired
	}
	return approval
}

func tokenExpired(approval Approval, now time.Time) bool {
	return approval.TokenExpiresAt != nil && now.After(*approval.TokenExpiresAt)
}

func findApprovalIndex(approvals []Approval, id string) int {
	for i, approval := range approvals {
		if approval.ID == id {
			return i
		}
	}
	return -1
}

func findApprovalByTokenHash(approvals []Approval, hash string) int {
	for i, approval := range approvals {
		if approval.TokenHash == hash {
			return i
		}
	}
	return -1
}

func trimAuditEvents(events []AuditEvent, limit int) []AuditEvent {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	return append([]AuditEvent(nil), events[len(events)-limit:]...)
}

func (s *ApprovalFileStore) load() (approvalState, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return approvalState{}, nil
	}
	if err != nil {
		return approvalState{}, fmt.Errorf("load operation approvals: %w", err)
	}
	if len(raw) == 0 {
		return approvalState{}, nil
	}

	var st approvalState
	if err := json.Unmarshal(raw, &st); err != nil {
		return approvalState{}, fmt.Errorf("load operation approvals: %w", err)
	}
	return st, nil
}

func (s *ApprovalFileStore) save(st approvalState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("save operation approvals: %w", err)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("save operation approvals: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return fmt.Errorf("save operation approvals: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("save operation approvals: %w", err)
	}
	return nil
}

func (s *AuditFileStore) load() (auditState, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return auditState{}, nil
	}
	if err != nil {
		return auditState{}, fmt.Errorf("load operation audit: %w", err)
	}
	if len(raw) == 0 {
		return auditState{}, nil
	}

	var st auditState
	if err := json.Unmarshal(raw, &st); err != nil {
		return auditState{}, fmt.Errorf("load operation audit: %w", err)
	}
	return st, nil
}

func (s *AuditFileStore) save(st auditState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("save operation audit: %w", err)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("save operation audit: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return fmt.Errorf("save operation audit: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("save operation audit: %w", err)
	}
	return nil
}
