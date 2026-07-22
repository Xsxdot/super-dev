// resources.go 校验并原子 staging bundle 内的 js-debug 与 target-native Playwright 资源。
//
// 职责：
//   - 对文件或目录生成路径、mode 与内容绑定的规范 SHA-256
//   - 只在摘要精确匹配时复制到 disposable profile
//   - 拒绝 symlink、路径逃逸和运行期联网下载
//
// 边界：
//   - 不解析资源业务格式，也不选择 target
//   - 不覆盖已存在目标；profile 必须由本次 campaign 独占
package runtimevalidation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// ResourceSpec 声明一个 bundle 资源的来源、profile 目标和规范摘要。
type ResourceSpec struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
}

// ResourceReceipt 保存成功 staging 后的资源身份和文件数。
type ResourceReceipt struct {
	ID          string `json:"id"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	FileCount   int    `json:"file_count"`
}

// DigestPath 计算一个普通文件或无 symlink 目录树的规范 SHA-256。
//
// 参数：
//   - root: 待校验的文件或目录
//
// 返回：
//   - 绑定相对路径、permission mode 和内容的十六进制 SHA-256
//   - 读取失败或遇到 symlink/特殊文件时的错误
//
// 注意：目录遍历顺序固定，不受宿主文件系统枚举顺序影响。
func DigestPath(root string) (string, error) {
	hash := sha256.New()
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("resource root cannot be a symlink")
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("resource root is not a regular file or directory")
		}
		if err := digestFile(hash, root, filepath.Base(root), info.Mode()); err != nil {
			return "", err
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		entry, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("resource %s contains symlink %s", root, relative)
		}
		if entry.IsDir() {
			_, _ = fmt.Fprintf(hash, "dir\x00%s\x00%04o\x00", relative, entry.Mode().Perm())
			continue
		}
		if !entry.Mode().IsRegular() {
			return "", fmt.Errorf("resource %s contains special file %s", root, relative)
		}
		if err := digestFile(hash, path, relative, entry.Mode()); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// StageResources 校验并原子复制全部 bundle 资源到 disposable profile。
//
// 参数：
//   - profileRoot: 本次 campaign 独占的 profile 根目录
//   - specs: 已由 bundle manifest 绑定摘要的资源声明
//
// 返回：
//   - 每个成功资源的摘要和文件数 receipt
//   - 路径、摘要、复制、fsync 或目标冲突错误
//
// 注意：任一摘要 drift 时在写目标前失败；运行期不尝试下载替代资源。
func StageResources(profileRoot string, specs []ResourceSpec) ([]ResourceReceipt, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationResources").WithField("profile_root", profileRoot)
	log.WithField("resource_count", len(specs)).Info("开始 staging runtime validation bundle 资源")
	receipts := make([]ResourceReceipt, 0, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Source) == "" || strings.TrimSpace(spec.SHA256) == "" {
			return nil, fmt.Errorf("resource id, source and sha256 are required")
		}
		destination, err := secureDestination(profileRoot, spec.Destination)
		if err != nil {
			return nil, err
		}
		digest, err := DigestPath(spec.Source)
		if err != nil {
			log.WithErr(err).WithField("resource_id", spec.ID).Error("读取 bundle 资源失败")
			return nil, fmt.Errorf("digest resource %s: %w", spec.ID, err)
		}
		if !strings.EqualFold(digest, spec.SHA256) {
			log.WithFields(map[string]any{"resource_id": spec.ID, "expected_sha256": spec.SHA256, "actual_sha256": digest}).Error("bundle 资源 digest drift")
			return nil, fmt.Errorf("resource %s digest mismatch", spec.ID)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			return nil, fmt.Errorf("resource destination %s already exists", destination)
		}
		parent := filepath.Dir(destination)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, err
		}
		stagingRoot, err := os.MkdirTemp(parent, ".runtime-validation-resource-")
		if err != nil {
			return nil, err
		}
		staged := filepath.Join(stagingRoot, filepath.Base(destination))
		fileCount, copyErr := copyResource(spec.Source, staged)
		if copyErr == nil {
			copyErr = os.Rename(staged, destination)
		}
		removeErr := os.RemoveAll(stagingRoot)
		if copyErr != nil {
			return nil, fmt.Errorf("stage resource %s: %w", spec.ID, copyErr)
		}
		if removeErr != nil {
			return nil, fmt.Errorf("remove resource staging directory: %w", removeErr)
		}
		receipts = append(receipts, ResourceReceipt{ID: spec.ID, Destination: spec.Destination, SHA256: digest, FileCount: fileCount})
		log.WithFields(map[string]any{"resource_id": spec.ID, "sha256": digest, "file_count": fileCount}).Info("bundle 资源 staging 完成")
	}
	log.WithField("resource_count", len(receipts)).Info("runtime validation bundle 资源全部 staging 完成")
	return receipts, nil
}

func digestFile(destination io.Writer, path, relative string, mode os.FileMode) error {
	_, _ = fmt.Fprintf(destination, "file\x00%s\x00%04o\x00", relative, mode.Perm())
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(destination, file)
	return err
}

func secureDestination(root, relative string) (string, error) {
	cleaned := filepath.Clean(relative)
	if relative == "" || filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resource destination %q escapes profile", relative)
	}
	return filepath.Join(root, cleaned), nil
}

func copyResource(source, destination string) (int, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
			return 0, err
		}
		count := 0
		entries, err := os.ReadDir(source)
		if err != nil {
			return 0, err
		}
		for _, entry := range entries {
			childCount, err := copyResource(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()))
			if err != nil {
				return 0, err
			}
			count += childCount
		}
		return count, nil
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("resource %s is not a regular file", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	for _, candidate := range []error{copyErr, syncErr, closeErr} {
		if candidate != nil {
			return 0, candidate
		}
	}
	return 1, nil
}
