// registry.go 提供 provider 注册表与内置 provider 单例。
//
// 职责：按 service.language 索引 provider；Core() 暴露进程级单例供 codedebug/api 消费。
// 边界：不做 provider 能力判定，只做注册与查找。
package langruntime

import (
	"sort"
	"sync"

	"github.com/xsxdot/super-dev/agent/model"
)

// Registry 是按语言索引的 provider 注册表。
type Registry struct {
	mu        sync.RWMutex
	providers map[model.ServiceLanguage]Provider
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{providers: map[model.ServiceLanguage]Provider{}}
}

// Register 注册一个 provider；nil 或语言为空时忽略。
func (r *Registry) Register(provider Provider) {
	if provider == nil || provider.Language() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[provider.Language()] = provider
}

// Provider 按语言查找 provider。
func (r *Registry) Provider(language model.ServiceLanguage) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[language]
	return provider, ok
}

// Languages 返回已注册语言（字典序稳定）。
func (r *Registry) Languages() []model.ServiceLanguage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]model.ServiceLanguage, 0, len(r.providers))
	for language := range r.providers {
		out = append(out, language)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// core 是内置 provider 单例注册表；provider 实现必须无状态。
var core = func() *Registry {
	r := NewRegistry()
	r.Register(NewGoProvider())
	r.Register(NewNodeProvider())
	r.Register(NewPythonProvider())
	r.Register(NewJVMProvider(model.LanguageJava))
	r.Register(NewJVMProvider(model.LanguageKotlin))
	r.Register(NewNativeProvider(model.LanguageRust))
	r.Register(NewNativeProvider(model.LanguageCpp))
	return r
}()

// Core 返回内置 provider 注册表。
func Core() *Registry { return core }
