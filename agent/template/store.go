// Package template 中的 store.go 负责本地模板库的导入、草稿和发布。
//
// 职责：
//   - 管理 user/project 模板的草稿文件
//   - 发布不可变版本并记录内容 digest
//   - 导入外部 YAML 模板到 user 模板库
//
// 边界：
//   - 不执行模板 include 展开
//   - 不处理 HTTP 请求或前端 DTO
//   - 不直接修改 deployment 配置
package template

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// VersionedTemplate is a published template plus its digest and source.
type VersionedTemplate struct {
	Source   string   `json:"source"`
	Template Template `json:"template"`
	Digest   string   `json:"digest"`
}

// Store manages builtin, user, and project templates.
type Store struct {
	baseDir    string
	builtin    map[string]Template
	projectDir string
}

// NewStore creates a template store rooted at baseDir.
//
// 参数：
//   - baseDir: user 模板库根目录
//   - builtin: 内置模板表，key 由调用方决定
//   - projectDir: project 模板库所属项目目录，可为空
//
// 返回：
//   - 可用于导入、保存草稿和发布模板的 Store
//
// 注意：
//   - builtin 模板在后续列表/解析能力中使用，本任务只保留字段
func NewStore(baseDir string, builtin map[string]Template, projectDir string) *Store {
	if builtin == nil {
		builtin = map[string]Template{}
	}
	return &Store{baseDir: baseDir, builtin: builtin, projectDir: projectDir}
}

// ImportFile imports a user template YAML file.
//
// 参数：
//   - path: 外部模板 YAML 文件路径
//
// 返回：
//   - 发布后的版本模板及 digest
//   - 文件读取、解析、校验或同版本 digest 冲突错误
//
// 注意：
//   - 导入默认写入 user 模板源
func (s *Store) ImportFile(path string) (VersionedTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VersionedTemplate{}, err
	}
	var tpl Template
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		return VersionedTemplate{}, err
	}
	if err := Validate(tpl); err != nil {
		return VersionedTemplate{}, err
	}
	return s.writePublished("user", tpl)
}

// SaveDraft stores a draft template for a source.
//
// 参数：
//   - source: 模板来源，支持 user/user:// 与 project/project://
//   - tpl: 待保存草稿模板
//
// 返回：
//   - 保存失败或模板结构不合法时返回错误
//
// 注意：
//   - 草稿可覆盖；不可变约束只发生在发布版本时
func (s *Store) SaveDraft(source string, tpl Template) error {
	if err := Validate(tpl); err != nil {
		return err
	}
	path := filepath.Join(s.rootForSource(source), "drafts", tpl.ID+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(tpl)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// PublishDraft publishes a draft as an immutable version.
//
// 参数：
//   - source: 模板来源，支持 user/user:// 与 project/project://
//   - id: 草稿模板 ID
//   - version: 要发布的不可变版本号
//
// 返回：
//   - 发布后的版本模板及 digest
//   - 草稿不存在、解析失败或同版本 digest 冲突错误
//
// 注意：
//   - 发布时会以传入 version 覆盖草稿中的 Version，避免 UI 草稿残留旧版本
func (s *Store) PublishDraft(source, id, version string) (VersionedTemplate, error) {
	path := filepath.Join(s.rootForSource(source), "drafts", id+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return VersionedTemplate{}, err
	}
	var tpl Template
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		return VersionedTemplate{}, err
	}
	tpl.Version = version
	if err := Validate(tpl); err != nil {
		return VersionedTemplate{}, err
	}
	return s.writePublished(source, tpl)
}

// Resolve resolves a template URI to an immutable versioned template.
//
// 参数：
//   - uri: 模板 URI，如 builtin://go-binary-build 或 user://custom
//   - version: 固定版本号
//   - digest: 可选 digest 锁，非空时必须匹配
//
// 返回：
//   - 匹配的模板、来源和 digest
//   - 模板不存在、校验失败或 digest 不匹配时返回错误
func (s *Store) Resolve(uri, version, digest string) (VersionedTemplate, error) {
	source, id := parseTemplateURI(uri)
	if id == "" {
		return VersionedTemplate{}, fmt.Errorf("template uri %q is invalid", uri)
	}
	var tpl Template
	if source == "builtin" {
		got, ok := s.builtin[id]
		if !ok {
			return VersionedTemplate{}, fmt.Errorf("builtin template %q not found", id)
		}
		tpl = got
	} else {
		path := filepath.Join(s.rootForSource(source), "versions", id, version+".yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			return VersionedTemplate{}, err
		}
		if err := yaml.Unmarshal(data, &tpl); err != nil {
			return VersionedTemplate{}, err
		}
	}
	if err := Validate(tpl); err != nil {
		return VersionedTemplate{}, err
	}
	if version != "" && tpl.Version != version {
		return VersionedTemplate{}, fmt.Errorf("template %s version mismatch: want %s got %s", uri, version, tpl.Version)
	}
	gotDigest, err := Digest(tpl)
	if err != nil {
		return VersionedTemplate{}, err
	}
	if digest != "" && gotDigest != digest {
		return VersionedTemplate{}, fmt.Errorf("template %s@%s digest mismatch", uri, version)
	}
	return VersionedTemplate{Source: source, Template: tpl, Digest: gotDigest}, nil
}

func (s *Store) writePublished(source string, tpl Template) (VersionedTemplate, error) {
	digest, err := Digest(tpl)
	if err != nil {
		return VersionedTemplate{}, err
	}
	path := filepath.Join(s.rootForSource(source), "versions", tpl.ID, tpl.Version+".yaml")
	if existing, err := os.ReadFile(path); err == nil {
		var got Template
		if unmarshalErr := yaml.Unmarshal(existing, &got); unmarshalErr != nil {
			return VersionedTemplate{}, unmarshalErr
		}
		existingDigest, err := Digest(got)
		if err != nil {
			return VersionedTemplate{}, err
		}
		if existingDigest == digest {
			return VersionedTemplate{Source: normalizeSource(source), Template: got, Digest: digest}, nil
		}
		return VersionedTemplate{}, fmt.Errorf("template %s@%s already exists with different digest", tpl.ID, tpl.Version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return VersionedTemplate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return VersionedTemplate{}, err
	}
	data, err := yaml.Marshal(tpl)
	if err != nil {
		return VersionedTemplate{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return VersionedTemplate{}, err
	}
	return VersionedTemplate{Source: normalizeSource(source), Template: tpl, Digest: digest}, nil
}

func (s *Store) rootForSource(source string) string {
	switch normalizeSource(source) {
	case "project":
		if s.projectDir != "" {
			return filepath.Join(s.projectDir, ".superdev", "templates")
		}
		return filepath.Join(s.baseDir, "project_templates")
	default:
		return filepath.Join(s.baseDir, "templates", "user")
	}
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(strings.TrimSuffix(source, "://"))
	if source == "" {
		return "user"
	}
	return source
}

func parseTemplateURI(uri string) (string, string) {
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 {
		return "user", strings.TrimSpace(uri)
	}
	return normalizeSource(parts[0]), strings.TrimSpace(parts[1])
}
