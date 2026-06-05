// Package plugins 中的 archive_package.go 实现多文件归档插件。
//
// 职责：
//   - 按 files 清单把多个本地文件或目录写入同一个 tar.gz
//   - 使用 artifact 表示最终归档文件路径
//   - 校验归档内路径，避免写入穿越路径
//
// 边界：
//   - 不上传归档文件
//   - 不执行构建命令
//   - 不推导 artifact 命名规则
package plugins

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

// ArchivePackage creates a tar.gz artifact from an explicit files list.
type ArchivePackage struct{}

// NewArchivePackage creates ArchivePackage.
//
// 返回：
//   - archive_package 插件实例
func NewArchivePackage() *ArchivePackage { return &ArchivePackage{} }

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `archive_package`
func (p *ArchivePackage) Name() string { return "archive_package" }

// Validate checks archive_package step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - roles 非空、artifact/files 缺失、format 不支持或归档路径不安全时返回错误
func (p *ArchivePackage) Validate(step model.Step) error {
	if len(step.Roles) > 0 {
		return errors.New("archive_package does not accept roles")
	}
	if withString(step.With, "artifact") == "" {
		return errors.New("archive_package requires with.artifact")
	}
	format := withString(step.With, "format")
	if format != "" && format != "tar.gz" {
		return fmt.Errorf("archive_package only supports tar.gz format")
	}
	files, err := archivePackageFiles(step.With["files"])
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("archive_package requires with.files")
	}
	for _, item := range files {
		if item.From == "" {
			return errors.New("archive_package files[].from is required")
		}
		if err := validateArchivePackageTarget(item.To); err != nil {
			return err
		}
	}
	return nil
}

// Execute creates the configured tar.gz artifact.
//
// 参数：
//   - ctx: 插件运行上下文，用于取消目录遍历
//   - step: archive_package 步骤
//   - targets: 本插件忽略 targets，必须通过 Validate 保证 roles 为空
//
// 返回：
//   - 文件系统访问、创建或写入失败时返回错误
func (p *ArchivePackage) Execute(ctx *pipeline.RunContext, step model.Step, _ []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	artifact := withString(step.With, "artifact")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		return err
	}
	out, err := os.Create(artifact)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	files, err := archivePackageFiles(step.With["files"])
	if err != nil {
		return err
	}
	for _, item := range files {
		if err := writeArchivePackageItem(ctx, tw, item); err != nil {
			return err
		}
	}
	return nil
}

type archivePackageFile struct {
	From string
	To   string
}

func archivePackageFiles(raw interface{}) ([]archivePackageFile, error) {
	switch values := raw.(type) {
	case []interface{}:
		out := make([]archivePackageFile, 0, len(values))
		for _, value := range values {
			item, err := archivePackageFileFromValue(value)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case []map[string]interface{}:
		out := make([]archivePackageFile, 0, len(values))
		for _, value := range values {
			item, err := archivePackageFileFromMap(value)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case []map[string]string:
		out := make([]archivePackageFile, 0, len(values))
		for _, value := range values {
			out = append(out, archivePackageFile{From: value["from"], To: value["to"]})
		}
		return out, nil
	default:
		return nil, errors.New("archive_package requires with.files")
	}
}

func archivePackageFileFromValue(raw interface{}) (archivePackageFile, error) {
	switch value := raw.(type) {
	case map[string]interface{}:
		return archivePackageFileFromMap(value)
	case map[string]string:
		return archivePackageFile{From: value["from"], To: value["to"]}, nil
	case map[interface{}]interface{}:
		return archivePackageFile{
			From: archivePackageInterfaceMapString(value, "from"),
			To:   archivePackageInterfaceMapString(value, "to"),
		}, nil
	default:
		return archivePackageFile{}, fmt.Errorf("archive_package files item must be an object")
	}
}

func archivePackageFileFromMap(value map[string]interface{}) (archivePackageFile, error) {
	return archivePackageFile{
		From: archivePackageStringMapString(value, "from"),
		To:   archivePackageStringMapString(value, "to"),
	}, nil
}

func archivePackageStringMapString(value map[string]interface{}, key string) string {
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	return fmt.Sprint(raw)
}

func archivePackageInterfaceMapString(value map[interface{}]interface{}, key string) string {
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	return fmt.Sprint(raw)
}

func validateArchivePackageTarget(target string) error {
	clean := path.Clean(strings.ReplaceAll(target, "\\", "/"))
	if target == "" || clean == "." {
		return errors.New("archive_package files[].to is required")
	}
	if filepath.IsAbs(target) || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("archive_package files[].to %q contains path traversal", target)
	}
	return nil
}

func writeArchivePackageItem(ctx *pipeline.RunContext, tw *tar.Writer, item archivePackageFile) error {
	info, err := os.Stat(item.From)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return writeTarFile(tw, item.To, item.From, info)
	}
	return filepath.WalkDir(item.From, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Context.Done():
			return ctx.Context.Err()
		default:
		}
		if path == item.From {
			return nil
		}
		return writeArchivePackageWalkEntry(tw, item.From, item.To, path, d)
	})
}

func writeArchivePackageWalkEntry(tw *tar.Writer, source string, targetRoot string, path string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(source, path)
	if err != nil {
		return err
	}
	name := filepath.ToSlash(filepath.Join(targetRoot, rel))
	if err := validateArchivePackageTarget(name); err != nil {
		return err
	}
	if !d.IsDir() {
		return writeTarFile(tw, name, path, info)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	return tw.WriteHeader(header)
}
