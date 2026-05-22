// Package collector 提供按 (name, type) 启停远端日志采集任务的能力。
//
// 职责：
//   - 校验 name 仅允许安全字符，避免命令注入
//   - 按 type 选择命令模板（journalctl / docker）
//   - 生成稳定的 CollectorID（hash(name+type)），保证幂等
//
// 边界：
//   - 不执行命令，仅返回 argv；执行由 collector.Manager + process.Runner 负责
//   - 命令模板写死在代码中，调用方不能传入任意命令
package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/superdev/agent/model"
)

// nameRegex 限制 name 只允许字母、数字、点、下划线、连字符。
//
// 不允许：空格、引号、反引号、$、;、|、&、/、\、< >、( )、?、*、:、,、!
// 长度限制：1-128。
var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

// ErrInvalidName 表示传入的 name 含非法字符或长度不符合要求。
var ErrInvalidName = errors.New("invalid name: only [a-zA-Z0-9._-] allowed, length 1-128")

// ErrUnsupportedType 表示 LogSourceType 不在允许的枚举范围内。
var ErrUnsupportedType = errors.New("unsupported log source type")

// argRegex 限制每个额外参数只允许安全字符。
// 参数名必须以 -- 或 - 开头；参数值必须至少包含一个数字或非字母字符（防止纯命令名注入）。
var argRegex = regexp.MustCompile(`^(-{1,2}[a-zA-Z][a-zA-Z0-9-]*)$|^([a-zA-Z0-9._/:@-]*[0-9._/:@-][a-zA-Z0-9._/:@-]*)$`)

// ErrInvalidArg 表示 extraArgs 中某个参数含非法字符或格式不符合要求。
var ErrInvalidArg = errors.New("invalid extra arg: only safe flag/value characters allowed")

// ValidateName 校验 name 是否满足 nameRegex。
//
// 参数：
//   - name: 待校验的 systemd 单元名或 docker 容器名
//
// 返回：
//   - 合法返回 nil；否则返回 ErrInvalidName
func ValidateName(name string) error {
	if !nameRegex.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// BuildCommand 按 type 模板组合 argv，name 作为参数（不进 shell 解析）。
//
// 参数：
//   - t: 采集类型，必须在 LogSourceType 枚举内
//   - name: 校验通过的服务名/容器名
//   - extraArgs: 额外追加参数，每个元素须通过 argRegex 校验；nil 表示无额外参数
//
// 返回：
//   - argv 切片，调用方用 exec.Command(argv[0], argv[1:]...) 执行
//   - type 不支持、name 非法或 extraArgs 含非法字符时返回错误
func BuildCommand(t model.LogSourceType, name string, extraArgs []string) ([]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	for _, arg := range extraArgs {
		if !argRegex.MatchString(arg) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidArg, arg)
		}
	}
	var base []string
	switch t {
	case model.LogSourceTypeJournalctl:
		base = []string{"journalctl", "-fu", name, "-o", "cat", "--no-pager"}
	case model.LogSourceTypeDocker:
		base = []string{"docker", "logs", "-f", name}
	default:
		return nil, ErrUnsupportedType
	}
	return append(base, extraArgs...), nil
}

// CollectorID 生成稳定的 collector ID,相同 (name, type) 总是返回同一 ID。
//
// 使用 sha256 前 16 字节的 hex 编码，保证 ID 内只含 [0-9a-f]。
func CollectorID(name string, t model.LogSourceType) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", t, name)))
	return hex.EncodeToString(h[:16])
}
