// Package config provides project configuration and registry management.
//
// 职责：
//   - 管理用户添加的项目路径注册表
//   - 持久化项目路径到 JSON 文件
//   - 提供线程安全的 Add、Remove、List 操作
//
// 边界：
//   - 只负责路径的存储和管理，不验证路径的有效性
//   - 不处理项目配置，仅处理路径列表
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Registry 管理项目路径的注册表，支持持久化到 JSON 文件。
type Registry struct {
	mu   sync.Mutex
	path string
}

// NewRegistry 创建一个新的项目注册表。
//
// 参数：
//   - path: JSON 文件的完整路径，用于持久化项目列表
//
// 返回：
//   - 初始化的 Registry 实例
func NewRegistry(path string) *Registry {
	return &Registry{path: path}
}

// List 返回所有已注册的项目路径列表。
//
// 返回：
//   - 项目路径数组，如果注册表为空则返回空切片
func (r *Registry) List() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths, _ := r.load()
	return paths
}

// Add 将项目路径添加到注册表。
//
// 参数：
//   - rootPath: 项目的根路径
//
// 返回：
//   - 如果添加失败返回错误
//
// 注意：
//   - 自动去重，相同路径不会重复添加
//   - 操作是原子的，加锁保证并发安全
func (r *Registry) Add(rootPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths, _ := r.load()
	if !slices.Contains(paths, rootPath) {
		paths = append(paths, rootPath)
	}
	return r.save(paths)
}

// Remove 从注册表中删除项目路径。
//
// 参数：
//   - rootPath: 要删除的项目路径
//
// 返回：
//   - 如果删除失败返回错误
//
// 注意：
//   - 如果路径不存在，操作仍然成功（幂等）
//   - 操作是原子的，加锁保证并发安全
func (r *Registry) Remove(rootPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths, _ := r.load()
	paths = slices.DeleteFunc(paths, func(p string) bool { return p == rootPath })
	return r.save(paths)
}

// load 从 JSON 文件读取项目路径列表。
//
// 返回：
//   - 项目路径数组和错误信息
//   - 如果文件不存在，返回 nil 而不是错误
func (r *Registry) load() ([]string, error) {
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

// save 将项目路径列表写入 JSON 文件。
//
// 参数：
//   - paths: 项目路径数组
//
// 返回：
//   - 如果保存失败返回错误
//
// 注意：
//   - 自动创建必要的目录
func (r *Registry) save(paths []string) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, data, 0o644)
}
