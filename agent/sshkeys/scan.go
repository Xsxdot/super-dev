// Package sshkeys 扫描本机 SSH 密钥目录，列出可用于登录的私钥候选。
//
// 职责：
//   - 枚举指定目录，按文件内容（而非文件名）判定哪些是 SSH 私钥
//   - 提取密钥类型与是否带 passphrase，供 UI 展示
//
// 边界：
//   - 只读，不修改、不移动、不解密任何密钥文件
//   - 不读取密钥全文，只读头部足以判定的部分；私钥内容由调用方在真正需要时自行读取
//   - 不解析 ~/.ssh/config，与 sshconfig 包无耦合
package sshkeys

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/hostpaths"
)

// maxKeyFileSize 是参与判定的文件大小上限。私钥不会达到这个量级，
// 超过即视为无关文件，避免把大文件读进内存。
const maxKeyFileSize = 64 * 1024

// headerReadSize 是判定所需读取的头部字节数。BEGIN 行、Proc-Type 与
// OpenSSH KDF 段都落在文件开头，因此无需读入私钥全文。
const headerReadSize = 4 * 1024

// skipNames 是明确不可能是私钥的同目录文件。
// 仅为省去无谓读取，真正的判据是下方的 BEGIN 行检测。
var skipNames = map[string]bool{
	"config":          true,
	"authorized_keys": true,
	"environment":     true,
	"rc":              true,
}

// Key 是扫描到的一个私钥候选。
type Key struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Encrypted bool   `json:"encrypted"`
}

// DefaultDir 返回本机默认的 SSH 密钥目录 ~/.ssh。
//
// 返回：
//   - 绝对路径
//   - 无法确定 home 目录时返回错误
func DefaultDir() (string, error) {
	home, err := hostpaths.UserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh"), nil
}

// Scan 扫描 dir 下的 SSH 私钥候选。
//
// 参数：
//   - dir: 待扫描目录，通常来自 DefaultDir()
//
// 返回：
//   - 按文件名升序排列的候选列表；无候选时返回空切片而非 nil
//   - 目录无法枚举时返回错误；目录不存在**不是**错误（首次使用的正常场景）
//
// 注意：
//   - 单个文件读取失败会被跳过而非中断整体扫描——~/.ssh 下有一个坏文件
//     不应让整个列表不可用
//   - 日志只记录数量，绝不记录路径或密钥内容
func Scan(dir string) ([]Key, error) {
	log := logger.GetLogger().WithEntryName("SSHKeys").WithField("operation", "scan")

	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录不存在是首次使用的正常场景，返回空列表让 UI 显示「未找到私钥」。
		if errors.Is(err, fs.ErrNotExist) {
			log.Info("SSH 密钥目录不存在，返回空候选列表")
			return []Key{}, nil
		}
		log.Errorf("枚举 SSH 密钥目录失败: %v", err)
		return nil, err
	}

	keys := make([]Key, 0, len(entries))
	skipped := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".pub") || strings.HasPrefix(name, "known_hosts") || skipNames[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			skipped++
			continue
		}
		if info.Size() > maxKeyFileSize {
			continue
		}
		key, ok, err := inspect(filepath.Join(dir, name), name)
		if err != nil {
			// 坏文件（权限不足等）跳过即可，不能让整个列表不可用。
			skipped++
			continue
		}
		if ok {
			keys = append(keys, key)
		}
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	if skipped > 0 {
		log.Warnf("扫描 SSH 密钥完成: 候选 %d 个, 跳过不可读文件 %d 个", len(keys), skipped)
	} else {
		log.Infof("扫描 SSH 密钥完成: 候选 %d 个", len(keys))
	}
	return keys, nil
}

// inspect 读取文件头部并判定其是否为私钥。
//
// 返回：
//   - 判定出的 Key（ok 为 false 时无意义）
//   - 是否为私钥
//   - 读取失败的错误，由调用方决定跳过
func inspect(path, name string) (Key, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return Key{}, false, err
	}
	defer file.Close()

	buf := make([]byte, headerReadSize)
	n, err := file.Read(buf)
	// 读到内容的同时可能返回 io.EOF（文件短于缓冲区），此时不算失败。
	if err != nil && !errors.Is(err, io.EOF) {
		return Key{}, false, err
	}
	head := buf[:n]

	begin, ok := beginLine(head)
	if !ok {
		return Key{}, false, nil
	}

	// 如果路径在用户 home 下，转换为 ~/ 前缀形式。
	// 后端 ReadPrivateKey 与 expandHome 均已处理展开，前端展示也更干净。
	// 保存时前端原样回传 ~/ 形式，链路自洽。
	displayPath := path
	home, err := hostpaths.UserHome()
	if err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		displayPath = "~" + path[len(home):]
	}

	return Key{
		Path:      displayPath,
		Name:      name,
		Type:      keyType(begin),
		Encrypted: isEncrypted(head, begin),
	}, true, nil
}

// beginLine 提取 "-----BEGIN ... PRIVATE KEY-----" 行。
// 这是判定私钥的唯一真正依据，文件名规则只用于省去读取。
func beginLine(head []byte) (string, bool) {
	for _, raw := range bytes.Split(head, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY-----") {
			return line, true
		}
		// 私钥的 BEGIN 必须在首个非空行；否则是别的文件恰好含有该字样。
		return "", false
	}
	return "", false
}

// keyType 由 BEGIN 行推断密钥类型，无法识别时返回 "unknown"。
// PKCS8 格式（BEGIN PRIVATE KEY / BEGIN ENCRYPTED PRIVATE KEY）
// 和 OpenSSH v1 格式（BEGIN OPENSSH PRIVATE KEY）不在 BEGIN 行标记算法，
// 故无法推断其密钥类型，这是这些格式的已知限制。
// OpenSSH 格式虽然内部可能是 RSA、ECDSA 或 ed25519，但 BEGIN 行无法区分，
// 所以返回 "openssh" 表示格式而非算法，由调用方或用户确认实际算法。
func keyType(begin string) string {
	switch {
	case strings.Contains(begin, "RSA"):
		return "rsa"
	case strings.Contains(begin, "EC"):
		return "ecdsa"
	case strings.Contains(begin, "DSA"):
		return "dsa"
	case strings.Contains(begin, "OPENSSH"):
		return "openssh"
	default:
		return "unknown"
	}
}

// isEncrypted 判定私钥是否带 passphrase。
//
// 三种格式各有标记：传统 PEM 用 Proc-Type: 4,ENCRYPTED；PKCS8 在 BEGIN 行
// 直接标记加密；OpenSSH 新格式在 base64 体内记录 KDF 名，none 表示未加密。
// 带 passphrase 的密钥 agent 建隧道时会失败，故提前标注供 UI 提示。
func isEncrypted(head []byte, begin string) bool {
	if bytes.Contains(head, []byte("Proc-Type: 4,ENCRYPTED")) {
		return true
	}
	// PKCS8 加密私钥在 BEGIN 行直接标记加密，不依赖 Proc-Type 头。
	// 这是判定该格式加密状态的唯一标记。
	if strings.Contains(begin, "ENCRYPTED PRIVATE KEY") {
		return true
	}
	if !strings.Contains(begin, "OPENSSH") {
		return false
	}
	// OpenSSH 未加密密钥的 KDF 为 "none"，其 base64 体开头恒为固定前缀
	// b3BlbnNzaC1rZXktdjEAAAAABG5vbmU...（"openssh-key-v1\0" + "none"）。
	return !bytes.Contains(head, []byte("AAAABG5vbmU"))
}
