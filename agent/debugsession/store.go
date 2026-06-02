// Package debugsession 持久化本机排障会话。
//
// 职责：
//   - 管理项目级 debug session 和事件流
//   - 将会话持久化到 agent data dir 下的 JSON 文件
//   - 限制单条事件数据大小，避免日志结果撑爆本地文件
//
// 边界：
//   - 不校验 project/deployment 是否存在，由 api 层负责
//   - 不做日志查询或根因推断
//   - 不跨机器同步
package debugsession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const maxEventDataBytes = 32 * 1024

const (
	StatusOpen   = "open"
	StatusClosed = "closed"

	EventNote         = "note"
	EventToolCall     = "tool_call"
	EventObservation  = "observation"
	EventLogAnalysis  = "log_analysis"
	EventStatusChange = "status_change"

	ActorUser      = "user"
	ActorAssistant = "assistant"
	ActorSystem    = "system"
)

var (
	ErrSessionNotFound = errors.New("debug session not found")
	ErrSessionClosed   = errors.New("debug session is closed")
	ErrEventTooLarge   = errors.New("debug session event data is too large")
	ErrInvalidEvent    = errors.New("debug session event is invalid")
)

// Session 记录一次项目级本机排障会话。
type Session struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	ProjectName  string     `json:"project_name"`
	EnvName      string     `json:"env_name,omitempty"`
	ServiceID    string     `json:"service_id,omitempty"`
	ServiceName  string     `json:"service_name,omitempty"`
	DeploymentID string     `json:"deployment_id,omitempty"`
	Title        string     `json:"title"`
	Question     string     `json:"question"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// Event 记录排障会话中的一条结构化事件。
type Event struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Type      string         `json:"type"`
	Actor     string         `json:"actor"`
	Summary   string         `json:"summary"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// CreateRequest 描述创建排障会话所需的上下文。
type CreateRequest struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	EnvName      string `json:"env_name,omitempty"`
	ServiceID    string `json:"service_id,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Title        string `json:"title"`
	Question     string `json:"question"`
}

// AppendEventRequest 描述追加到排障会话的事件。
type AppendEventRequest struct {
	Type    string         `json:"type"`
	Actor   string         `json:"actor"`
	Summary string         `json:"summary"`
	Data    map[string]any `json:"data,omitempty"`
}

// ListFilter 描述排障会话列表过滤条件。
type ListFilter struct {
	ProjectID string
	Status    string
	Limit     int
}

// Store 定义排障会话持久化能力。
type Store interface {
	Create(context.Context, CreateRequest) (Session, Event, error)
	List(context.Context, ListFilter) ([]Session, error)
	Get(context.Context, string, int) (Session, []Event, error)
	AppendEvent(context.Context, string, AppendEventRequest) (Event, error)
	Close(context.Context, string, string) (Session, Event, error)
}

// FileStore 将排障会话保存为单个本机 JSON 文件。
type FileStore struct {
	path string
	mu   sync.Mutex
}

type state struct {
	Sessions []Session `json:"sessions"`
	Events   []Event   `json:"events"`
}

// NewFileStore 创建使用指定 JSON 文件路径的排障会话 Store。
//
// 参数：
//   - path: 会话状态文件完整路径
//
// 返回：
//   - 基于本机文件的 Store
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Create 创建一条打开状态的排障会话，并写入初始状态变更事件。
//
// 参数：
//   - ctx: 上下文，当前实现不阻塞外部 I/O，仅保留接口一致性
//   - req: 会话归属和问题描述
//
// 返回：
//   - 新会话
//   - 初始状态变更事件
//   - 错误信息
//
// 注意：
//   - project 是否真实存在由 api 层负责，这里只校验必填字段
func (s *FileStore) Create(ctx context.Context, req CreateRequest) (Session, Event, error) {
	_ = ctx
	req = normalizeCreateRequest(req)
	if req.ProjectID == "" || req.ProjectName == "" || req.Title == "" || req.Question == "" {
		return Session{}, Event{}, ErrInvalidEvent
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Session{}, Event{}, err
	}

	now := time.Now().UTC()
	session := Session{
		ID:           newID("ds"),
		ProjectID:    req.ProjectID,
		ProjectName:  req.ProjectName,
		EnvName:      req.EnvName,
		ServiceID:    req.ServiceID,
		ServiceName:  req.ServiceName,
		DeploymentID: req.DeploymentID,
		Title:        req.Title,
		Question:     req.Question,
		Status:       StatusOpen,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	event := Event{
		ID:        newID("dse"),
		SessionID: session.ID,
		Type:      EventStatusChange,
		Actor:     ActorSystem,
		Summary:   "debug session opened",
		Data:      map[string]any{"status": StatusOpen},
		CreatedAt: now,
	}

	st.Sessions = append(st.Sessions, session)
	st.Events = append(st.Events, event)
	if err := s.save(st); err != nil {
		return Session{}, Event{}, err
	}
	return session, event, nil
}

// List 查询排障会话列表。
//
// 参数：
//   - ctx: 上下文，当前实现不阻塞外部 I/O，仅保留接口一致性
//   - filter: project、状态和数量限制
//
// 返回：
//   - 按 UpdatedAt 倒序排列的会话列表
//   - 错误信息
func (s *FileStore) List(ctx context.Context, filter ListFilter) ([]Session, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return nil, err
	}

	projectID := strings.TrimSpace(filter.ProjectID)
	status := strings.TrimSpace(filter.Status)
	out := make([]Session, 0, len(st.Sessions))
	for _, session := range st.Sessions {
		if projectID != "" && session.ProjectID != projectID {
			continue
		}
		if status != "" && session.Status != status {
			continue
		}
		out = append(out, session)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Get 查询单个排障会话及其事件。
//
// 参数：
//   - ctx: 上下文，当前实现不阻塞外部 I/O，仅保留接口一致性
//   - id: 会话 ID
//   - limit: 事件数量限制，正数时生效
//
// 返回：
//   - 会话
//   - 按 CreatedAt 升序排列的事件列表
//   - 错误信息
func (s *FileStore) Get(ctx context.Context, id string, limit int) (Session, []Event, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Session{}, nil, err
	}

	session, ok := findSession(st.Sessions, strings.TrimSpace(id))
	if !ok {
		return Session{}, nil, ErrSessionNotFound
	}
	events := make([]Event, 0)
	for _, event := range st.Events {
		if event.SessionID == session.ID {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return session, events, nil
}

// AppendEvent 追加一条普通排障事件。
//
// 参数：
//   - ctx: 上下文，当前实现不阻塞外部 I/O，仅保留接口一致性
//   - sessionID: 会话 ID
//   - req: 事件内容
//
// 返回：
//   - 新事件
//   - 错误信息
//
// 注意：
//   - 已关闭会话拒绝普通事件，避免 AI 在结论归档后继续污染记录
func (s *FileStore) AppendEvent(ctx context.Context, sessionID string, req AppendEventRequest) (Event, error) {
	_ = ctx
	req = normalizeAppendEventRequest(req)
	if err := validateAppendEvent(req); err != nil {
		return Event{}, err
	}
	if err := ensureEventDataSize(req.Data); err != nil {
		return Event{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Event{}, err
	}
	idx := findSessionIndex(st.Sessions, strings.TrimSpace(sessionID))
	if idx < 0 {
		return Event{}, ErrSessionNotFound
	}
	if st.Sessions[idx].Status == StatusClosed {
		return Event{}, ErrSessionClosed
	}

	now := time.Now().UTC()
	event := Event{
		ID:        newID("dse"),
		SessionID: st.Sessions[idx].ID,
		Type:      req.Type,
		Actor:     req.Actor,
		Summary:   req.Summary,
		Data:      req.Data,
		CreatedAt: now,
	}
	st.Sessions[idx].UpdatedAt = now
	st.Events = append(st.Events, event)
	if err := s.save(st); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Close 关闭排障会话。
//
// 参数：
//   - ctx: 上下文，当前实现不阻塞外部 I/O，仅保留接口一致性
//   - sessionID: 会话 ID
//   - summary: 关闭原因摘要
//
// 返回：
//   - 关闭后的会话
//   - 状态变更事件；如果会话已关闭则返回空事件
//   - 错误信息
//
// 注意：
//   - 关闭操作幂等，重复关闭不会重复写入状态变更事件
func (s *FileStore) Close(ctx context.Context, sessionID string, summary string) (Session, Event, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.load()
	if err != nil {
		return Session{}, Event{}, err
	}
	idx := findSessionIndex(st.Sessions, strings.TrimSpace(sessionID))
	if idx < 0 {
		return Session{}, Event{}, ErrSessionNotFound
	}
	if st.Sessions[idx].Status == StatusClosed {
		return st.Sessions[idx], Event{}, nil
	}

	now := time.Now().UTC()
	st.Sessions[idx].Status = StatusClosed
	st.Sessions[idx].UpdatedAt = now
	st.Sessions[idx].ClosedAt = &now

	closeSummary := strings.TrimSpace(summary)
	if closeSummary == "" {
		closeSummary = "debug session closed"
	}
	event := Event{
		ID:        newID("dse"),
		SessionID: st.Sessions[idx].ID,
		Type:      EventStatusChange,
		Actor:     ActorSystem,
		Summary:   closeSummary,
		Data:      map[string]any{"status": StatusClosed},
		CreatedAt: now,
	}
	st.Events = append(st.Events, event)
	if err := s.save(st); err != nil {
		return Session{}, Event{}, err
	}
	return st.Sessions[idx], event, nil
}

func normalizeCreateRequest(req CreateRequest) CreateRequest {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.EnvName = strings.TrimSpace(req.EnvName)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.ServiceName = strings.TrimSpace(req.ServiceName)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	req.Title = strings.TrimSpace(req.Title)
	req.Question = strings.TrimSpace(req.Question)
	return req
}

func normalizeAppendEventRequest(req AppendEventRequest) AppendEventRequest {
	req.Type = strings.TrimSpace(req.Type)
	req.Actor = strings.TrimSpace(req.Actor)
	req.Summary = strings.TrimSpace(req.Summary)
	return req
}

func validateAppendEvent(req AppendEventRequest) error {
	if req.Summary == "" {
		return ErrInvalidEvent
	}
	switch req.Type {
	case EventNote, EventToolCall, EventObservation, EventLogAnalysis:
	default:
		return ErrInvalidEvent
	}
	switch req.Actor {
	case ActorUser, ActorAssistant, ActorSystem:
	default:
		return ErrInvalidEvent
	}
	return nil
}

func ensureEventDataSize(data map[string]any) error {
	if data == nil {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if len(raw) > maxEventDataBytes {
		return ErrEventTooLarge
	}
	return nil
}

func findSession(sessions []Session, id string) (Session, bool) {
	for _, session := range sessions {
		if session.ID == id {
			return session, true
		}
	}
	return Session{}, false
}

func findSessionIndex(sessions []Session, id string) int {
	for i, session := range sessions {
		if session.ID == id {
			return i
		}
	}
	return -1
}

func (s *FileStore) load() (state, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("load debug sessions: %w", err)
	}
	if len(raw) == 0 {
		return state{}, nil
	}

	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return state{}, fmt.Errorf("load debug sessions: %w", err)
	}
	return st, nil
}

func (s *FileStore) save(st state) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("save debug sessions: %w", err)
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("save debug sessions: %w", err)
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return fmt.Errorf("save debug sessions: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("save debug sessions: %w", err)
	}
	return nil
}

func newID(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("generate debug session id: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}
