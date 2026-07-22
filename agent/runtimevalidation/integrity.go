// integrity.go 生成并验证 runtime validation bundle 的 sidecar→manifest→payload 链。
//
// 职责：
//   - 为每个 payload 文件绑定相对路径、mode、size 和 SHA-256
//   - 先校验 manifest sidecar，再解析 target，最后精确比对 payload 全集
//   - 拒绝 symlink、missing/extra/hash/mode drift 和 target mismatch
//
// 边界：
//   - 外部 archive checksum 只证明传输完整性，不是来源签名
//   - package_verified 不参与原生 target PASS 判定
package runtimevalidation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const (
	bundleManifestFilename = "bundle-manifest.json"
	bundleSidecarFilename  = "bundle-manifest.sha256"
	bundleManifestKind     = "superdev.runtime-validation.bundle"
	bundleManifestVersion  = 1
)

// BundleFileIdentity 绑定一个 bundle payload 文件的规范身份。
type BundleFileIdentity struct {
	Path   string `json:"path"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BundleManifest 是 target 与全部 payload 文件的 authoritative package 清单。
type BundleManifest struct {
	SchemaVersion int                  `json:"schema_version"`
	Kind          string               `json:"kind"`
	Target        Target               `json:"target"`
	Files         []BundleFileIdentity `json:"files"`
}

// BundleReceipt 记录已完成三层校验的 bundle identity。
type BundleReceipt struct {
	Target         Target `json:"target"`
	ManifestSHA256 string `json:"manifest_sha256"`
	FileCount      int    `json:"file_count"`
}

// CreateBundleManifest 扫描尚未写 manifest/sidecar 的 bundle payload。
func CreateBundleManifest(root string, target Target) (BundleManifest, error) {
	if !supportedTarget(target) {
		return BundleManifest{}, fmt.Errorf("unsupported bundle target %s/%s", target.OS, target.Architecture)
	}
	files, err := scanBundlePayload(root, target)
	if err != nil {
		return BundleManifest{}, err
	}
	if len(files) == 0 {
		return BundleManifest{}, fmt.Errorf("bundle payload is empty")
	}
	return BundleManifest{SchemaVersion: bundleManifestVersion, Kind: bundleManifestKind, Target: target, Files: files}, nil
}

// WriteBundleManifest 先原子写 manifest，再原子写其 SHA-256 sidecar。
func WriteBundleManifest(root string, manifest BundleManifest) (BundleReceipt, error) {
	if err := validateBundleManifest(manifest); err != nil {
		return BundleReceipt{}, err
	}
	manifestPath := filepath.Join(root, bundleManifestFilename)
	sidecarPath := filepath.Join(root, bundleSidecarFilename)
	for _, path := range []string{manifestPath, sidecarPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return BundleReceipt{}, fmt.Errorf("bundle integrity file already exists: %s", path)
		}
	}
	if err := atomicWriteJSON(manifestPath, manifest, 0o600); err != nil {
		return BundleReceipt{}, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return BundleReceipt{}, err
	}
	digest := sha256.Sum256(raw)
	digestText := hex.EncodeToString(digest[:])
	if err := atomicWriteBytes(sidecarPath, []byte(digestText+"  "+bundleManifestFilename+"\n"), 0o600); err != nil {
		return BundleReceipt{}, err
	}
	receipt := BundleReceipt{Target: manifest.Target, ManifestSHA256: digestText, FileCount: len(manifest.Files)}
	logger.GetLogger().WithEntryName("RuntimeValidationIntegrity").WithFields(map[string]any{"target": manifest.Target.String(), "manifest_sha256": digestText, "file_count": len(manifest.Files)}).Info("runtime validation bundle manifest 与 sidecar 已写入")
	return receipt, nil
}

// VerifyBundle 按 sidecar→manifest→payload 顺序验证 bundle，且不访问 foundation。
func VerifyBundle(root string, expectedTarget Target) (BundleReceipt, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationIntegrity").WithFields(map[string]any{"bundle_root": root, "expected_target": expectedTarget.String()})
	log.Info("开始验证 runtime validation bundle sidecar、manifest 与 payload")
	sidecar, err := os.ReadFile(filepath.Join(root, bundleSidecarFilename))
	if err != nil {
		return BundleReceipt{}, fmt.Errorf("read bundle manifest sidecar: %w", err)
	}
	fields := strings.Fields(string(sidecar))
	if len(fields) != 2 || fields[1] != bundleManifestFilename || len(fields[0]) != sha256.Size*2 {
		return BundleReceipt{}, fmt.Errorf("bundle manifest sidecar is invalid")
	}
	manifestRaw, err := os.ReadFile(filepath.Join(root, bundleManifestFilename))
	if err != nil {
		return BundleReceipt{}, fmt.Errorf("read bundle manifest: %w", err)
	}
	digest := sha256.Sum256(manifestRaw)
	actualManifestDigest := hex.EncodeToString(digest[:])
	if !strings.EqualFold(fields[0], actualManifestDigest) {
		return BundleReceipt{}, fmt.Errorf("bundle manifest sidecar digest mismatch")
	}
	var manifest BundleManifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestRaw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return BundleReceipt{}, fmt.Errorf("decode bundle manifest: %w", err)
	}
	if err := validateBundleManifest(manifest); err != nil {
		return BundleReceipt{}, err
	}
	if manifest.Target != expectedTarget {
		return BundleReceipt{}, fmt.Errorf("bundle target %s does not match expected %s", manifest.Target.String(), expectedTarget.String())
	}
	actualFiles, err := scanBundlePayload(root, manifest.Target)
	if err != nil {
		return BundleReceipt{}, err
	}
	if len(actualFiles) != len(manifest.Files) {
		return BundleReceipt{}, fmt.Errorf("bundle payload file count=%d, want %d", len(actualFiles), len(manifest.Files))
	}
	for index := range manifest.Files {
		if actualFiles[index] != manifest.Files[index] {
			expected, actual := manifest.Files[index], actualFiles[index]
			return BundleReceipt{}, fmt.Errorf(
				"bundle payload drift at %s: expected mode=%s size=%d sha256=%s, actual mode=%s size=%d sha256=%s",
				expected.Path, expected.Mode, expected.Size, expected.SHA256, actual.Mode, actual.Size, actual.SHA256,
			)
		}
	}
	receipt := BundleReceipt{Target: manifest.Target, ManifestSHA256: actualManifestDigest, FileCount: len(manifest.Files)}
	log.WithFields(map[string]any{"manifest_sha256": actualManifestDigest, "file_count": len(manifest.Files)}).Info("runtime validation bundle 完整性验证通过")
	return receipt, nil
}

func validateBundleManifest(manifest BundleManifest) error {
	if manifest.SchemaVersion != bundleManifestVersion || manifest.Kind != bundleManifestKind || !supportedTarget(manifest.Target) || len(manifest.Files) == 0 {
		return fmt.Errorf("bundle manifest contract is invalid")
	}
	previous := ""
	for _, file := range manifest.Files {
		if file.Path == "" || file.Path <= previous || filepath.IsAbs(file.Path) || strings.HasPrefix(file.Path, "../") || len(file.SHA256) != sha256.Size*2 || file.Size < 0 {
			return fmt.Errorf("bundle manifest file identity is invalid: %s", file.Path)
		}
		if _, err := strconv.ParseUint(file.Mode, 8, 32); err != nil {
			return fmt.Errorf("bundle manifest file mode is invalid: %s", file.Path)
		}
		previous = file.Path
	}
	return nil
}

func scanBundlePayload(root string, target Target) ([]BundleFileIdentity, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("bundle root is not a directory")
	}
	files := make([]BundleFileIdentity, 0)
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
		relative = filepath.ToSlash(relative)
		if relative == bundleManifestFilename || relative == bundleSidecarFilename {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle payload cannot contain symlink %s", relative)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle payload contains special file %s", relative)
		}
		digest, err := hashRegularFile(path)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		// Windows 的 Go 文件系统不表达 POSIX executable bits，ZIP 解压后的普通文件统一为 0666。
		// manifest 必须按目标平台规范化，否则在 Unix builder 生成的 0644/0755 会造成真机伪漂移。
		if target.OS == "windows" {
			mode = 0o666
		}
		files = append(files, BundleFileIdentity{Path: relative, Mode: fmt.Sprintf("%04o", mode), Size: info.Size(), SHA256: digest})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func hashRegularFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func atomicWriteBytes(path string, value []byte, mode os.FileMode) error {
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(value)
	syncErr := file.Sync()
	closeErr := file.Close()
	for _, candidate := range []error{writeErr, syncErr, closeErr} {
		if candidate != nil {
			_ = os.Remove(temporary)
			return candidate
		}
	}
	if err := atomicReplace(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
