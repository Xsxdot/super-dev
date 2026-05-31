// Package plugins 中的 archive.go 实现本地归档插件。
//
// 职责：
//   - 将本地文件或目录打包为 tar.gz
//   - 校验 archive 插件必需参数
//
// 边界：
//   - 不上传归档文件
//   - 不执行构建命令
//   - 不删除旧归档产物
package plugins

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

// Archive creates a tar.gz file from a local file or directory.
type Archive struct{}

// NewArchive creates Archive.
//
// 返回：
//   - archive 插件实例
func NewArchive() *Archive { return &Archive{} }

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `archive`
func (p *Archive) Name() string { return "archive" }

// Validate checks archive step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - with.source 缺失或 format 非 tar.gz 时返回错误
func (p *Archive) Validate(step model.Step) error {
	if len(step.Roles) > 0 {
		return errors.New("archive does not accept roles")
	}
	if withString(step.With, "source") == "" {
		return errors.New("archive requires with.source")
	}
	format := withString(step.With, "format")
	if format != "" && format != "tar.gz" {
		return fmt.Errorf("archive only supports tar.gz format")
	}
	return nil
}

// Execute creates the configured tar.gz archive.
//
// 参数：
//   - ctx: 插件运行上下文，本插件当前只使用其 Context
//   - step: archive 步骤
//   - targets: 本插件忽略 targets，必须通过 Validate 保证 roles 为空
//
// 返回：
//   - 文件系统访问、创建或写入失败时返回错误
func (p *Archive) Execute(ctx *pipeline.RunContext, step model.Step, _ []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	source := withString(step.With, "source")
	dest := withString(step.With, "dest")
	if dest == "" {
		dest = filepath.Dir(source)
	}
	basename := withString(step.With, "basename")
	if basename == "" {
		basename = filepath.Base(source)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(dest, basename+".tar.gz")
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return writeTarFile(tw, filepath.Base(source), source, info)
	}
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Context.Done():
			return ctx.Context.Err()
		default:
		}
		if path == source {
			return nil
		}
		return writeTarEntry(tw, source, path, d)
	})
}

func writeTarEntry(tw *tar.Writer, source, path string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(source, path)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(rel)
	if d.IsDir() {
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		return nil
	}
	return writeTarFile(tw, header.Name, path, info)
}

func writeTarFile(tw *tar.Writer, name, path string, info fs.FileInfo) error {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(name)
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(tw, file)
	return err
}
