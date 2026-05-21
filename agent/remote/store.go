// Package remote 提供本机端 Host / LogSource 的持久化和控制能力。
//
// store.go 负责文件读写,文件位置由调用方注入(默认 ~/.superdev/{hosts.json,log_sources.json})。
//
// 边界：
//   - 不联系远端,不建立 SSH 隧道(由 controller.go 完成)
//   - 不校验 SSH 凭据合法性(无副作用,只读写 JSON)
//   - hosts.json 文件权限 0600,保护明文密码
package remote

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/superdev/agent/model"
)

const (
	defaultSSHPort         = 22
	defaultRemoteAgentPort = 57017
)

// ErrNotFound 表示按 ID 查找的资源不存在。
var ErrNotFound = errors.New("not found")

// Store 持久化 Host 和 LogSource。
//
// 线程安全:所有方法持有 mu。
type Store struct {
	mu             sync.Mutex
	hostsPath      string
	logSourcesPath string
}

// NewStore 创建 Store,文件路径必须可写。
func NewStore(hostsPath, logSourcesPath string) *Store {
	return &Store{hostsPath: hostsPath, logSourcesPath: logSourcesPath}
}

// ListHosts 返回所有 Host。
func (s *Store) ListHosts() ([]model.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadHosts()
}

// AddHost 分配 UUID 并持久化;返回填充了 ID 的 Host。
func (s *Store) AddHost(h model.Host) (model.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	applyHostDefaults(&h)
	hosts, err := s.loadHosts()
	if err != nil {
		return model.Host{}, err
	}
	hosts = append(hosts, h)
	if err := s.saveHosts(hosts); err != nil {
		return model.Host{}, err
	}
	return h, nil
}

// UpdateHost 覆盖指定 ID 的 Host;不存在时返回 ErrNotFound。
func (s *Store) UpdateHost(h model.Host) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	applyHostDefaults(&h)
	hosts, err := s.loadHosts()
	if err != nil {
		return err
	}
	for i, existing := range hosts {
		if existing.ID == h.ID {
			hosts[i] = h
			return s.saveHosts(hosts)
		}
	}
	return ErrNotFound
}

// RemoveHost 按 ID 删除;不存在视为成功(幂等)。
func (s *Store) RemoveHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.loadHosts()
	if err != nil {
		return err
	}
	filtered := hosts[:0]
	for _, h := range hosts {
		if h.ID != id {
			filtered = append(filtered, h)
		}
	}
	return s.saveHosts(filtered)
}

// ListLogSources 返回所有 LogSource。
func (s *Store) ListLogSources() ([]model.LogSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLogSources()
}

// AddLogSource 分配 UUID 并持久化。
func (s *Store) AddLogSource(ls model.LogSource) (model.LogSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ls.ID == "" {
		ls.ID = uuid.NewString()
	}
	list, err := s.loadLogSources()
	if err != nil {
		return model.LogSource{}, err
	}
	list = append(list, ls)
	if err := s.saveLogSources(list); err != nil {
		return model.LogSource{}, err
	}
	return ls, nil
}

// UpdateLogSource 覆盖指定 ID;不存在时返回 ErrNotFound。
func (s *Store) UpdateLogSource(ls model.LogSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLogSources()
	if err != nil {
		return err
	}
	for i, existing := range list {
		if existing.ID == ls.ID {
			list[i] = ls
			return s.saveLogSources(list)
		}
	}
	return ErrNotFound
}

// RemoveLogSource 按 ID 删除(幂等)。
func (s *Store) RemoveLogSource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLogSources()
	if err != nil {
		return err
	}
	filtered := list[:0]
	for _, ls := range list {
		if ls.ID != id {
			filtered = append(filtered, ls)
		}
	}
	return s.saveLogSources(filtered)
}

func (s *Store) loadHosts() ([]model.Host, error) {
	data, err := os.ReadFile(s.hostsPath)
	if os.IsNotExist(err) {
		return []model.Host{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []model.Host
	if err := json.Unmarshal(data, &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

func (s *Store) saveHosts(hosts []model.Host) error {
	if err := os.MkdirAll(filepath.Dir(s.hostsPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(hosts, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.hostsPath, data, 0o600)
}

func (s *Store) loadLogSources() ([]model.LogSource, error) {
	data, err := os.ReadFile(s.logSourcesPath)
	if os.IsNotExist(err) {
		return []model.LogSource{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []model.LogSource
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) saveLogSources(list []model.LogSource) error {
	if err := os.MkdirAll(filepath.Dir(s.logSourcesPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.logSourcesPath, data, 0o644)
}

func applyHostDefaults(h *model.Host) {
	if h.SSHPort == 0 {
		h.SSHPort = defaultSSHPort
	}
	if h.RemoteAgentPort == 0 {
		h.RemoteAgentPort = defaultRemoteAgentPort
	}
}
