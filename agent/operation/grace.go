// 本文件负责项目级审批豁免窗口的本机 JSON 持久化。
//
// 职责：
//   - 按项目记录“批准后 N 分钟免审”窗口
//   - 同项目重复授权时续期，不堆叠
//   - 查询活动窗口时顺手清理过期记录
//
// 边界：
//   - 不解析项目配置，不执行被豁免的操作
//   - 不判断 plan 是否带 ProjectID（由调用方保证）
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// GraceStore 持久化项目级审批豁免窗口。
type GraceStore interface {
	GrantGrace(ctx context.Context, projectID, grantedBy, fromApprovalID string, ttl time.Duration) (GraceGrant, error)
	ActiveGrace(ctx context.Context, projectID string) (GraceGrant, bool, error)
}

// GraceFileStore 将豁免窗口保存到本机 JSON 文件。
type GraceFileStore struct {
	path string
	mu   sync.Mutex
}

type graceState struct {
	Grants []GraceGrant `json:"grants"`
}

// NewGraceFileStore 创建一个使用指定 JSON 文件的豁免窗口存储。
func NewGraceFileStore(path string) *GraceFileStore {
	return &GraceFileStore{path: path}
}

// GrantGrace 为项目开启或续期豁免窗口。
//
// 参数：
//   - projectID: 目标项目，必填
//   - grantedBy: 决策人，用于审计
//   - fromApprovalID: 触发本次豁免的 approval ID
//   - ttl: 窗口时长，由调用方按当时设置换算
//
// 返回：
//   - 写入后的 grant；projectID 为空时返回错误
//
// 注意：
//   - 同项目已有窗口则续期（替换为最新窗口），不堆叠多条
func (s *GraceFileStore) GrantGrace(ctx context.Context, projectID, grantedBy, fromApprovalID string, ttl time.Duration) (GraceGrant, error) {
	projectID = trim(projectID)
	if projectID == "" {
		return GraceGrant{}, errors.New("grace grant requires project id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return GraceGrant{}, err
	}
	now := time.Now().UTC()
	grant := GraceGrant{
		ProjectID:   projectID,
		GrantedBy:   grantedBy,
		GrantedFrom: fromApprovalID,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	kept := make([]GraceGrant, 0, len(st.Grants)+1)
	for _, g := range st.Grants {
		if g.ProjectID == projectID {
			continue // 同项目旧窗口被新窗口替换，实现续期不堆叠
		}
		if g.ExpiresAt.After(now) {
			kept = append(kept, g) // 顺手保留其他项目仍有效的窗口
		}
	}
	kept = append(kept, grant)
	st.Grants = kept
	if err := s.save(st); err != nil {
		return GraceGrant{}, err
	}
	return grant, nil
}

// ActiveGrace 返回项目当前是否有未过期豁免窗口，并清理过期记录。
func (s *GraceFileStore) ActiveGrace(ctx context.Context, projectID string) (GraceGrant, bool, error) {
	projectID = trim(projectID)
	if projectID == "" {
		return GraceGrant{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return GraceGrant{}, false, err
	}
	now := time.Now().UTC()
	kept := make([]GraceGrant, 0, len(st.Grants))
	var hit GraceGrant
	found := false
	for _, g := range st.Grants {
		if !g.ExpiresAt.After(now) {
			continue // 过期，丢弃
		}
		kept = append(kept, g)
		if g.ProjectID == projectID {
			hit = g
			found = true
		}
	}
	if len(kept) != len(st.Grants) {
		st.Grants = kept
		if err := s.save(st); err != nil {
			return GraceGrant{}, false, err
		}
	}
	return hit, found, nil
}

func (s *GraceFileStore) load() (graceState, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return graceState{}, nil
	}
	if err != nil {
		return graceState{}, fmt.Errorf("read grace store: %w", err)
	}
	var st graceState
	if err := json.Unmarshal(data, &st); err != nil {
		return graceState{}, fmt.Errorf("parse grace store: %w", err)
	}
	return st, nil
}

func (s *GraceFileStore) save(st graceState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir grace dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal grace store: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o644)
}
