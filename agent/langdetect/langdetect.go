// Package langdetect 从项目目录探测服务的主要实现语言。
//
// 职责：
//   - 标记文件优先（go.mod / package.json / pyproject.toml / requirements.txt）
//   - command 启动前缀兜底（go / node / python）
//   - 返回空语言表示无法判定，由调用方决定是否提示用户手动选择
//
// 边界：
//   - 不读取文件内容做深度分析，只看文件是否存在与 command 首词
//   - 不访问网络或远端
package langdetect

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// markerFiles 按语言列出可判定的标记文件。
var markerFiles = map[model.ServiceLanguage][]string{
	model.LanguageGo:     {"go.mod"},
	model.LanguageNode:   {"package.json"},
	model.LanguagePython: {"pyproject.toml", "requirements.txt", "setup.py"},
}

// Detect 探测目录语言。标记文件优先，command 前缀兜底，都判不出返回空。
//
// 参数：
//   - dir: 服务工作目录绝对路径
//   - command: 服务启动命令，标记文件缺失时用首词兜底
func Detect(dir, command string) model.ServiceLanguage {
	if lang := detectByMarker(dir); lang != "" {
		return lang
	}
	return detectByCommand(command)
}

func detectByMarker(dir string) model.ServiceLanguage {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	// 固定探测顺序，保证多标记并存时结果稳定：go > node > python。
	for _, lang := range []model.ServiceLanguage{model.LanguageGo, model.LanguageNode, model.LanguagePython} {
		for _, name := range markerFiles[lang] {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return lang
			}
		}
	}
	return ""
}

func detectByCommand(command string) model.ServiceLanguage {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	head := fields[0]
	if idx := strings.LastIndex(head, "/"); idx >= 0 {
		head = head[idx+1:]
	}
	switch head {
	case "go":
		return model.LanguageGo
	case "node":
		return model.LanguageNode
	case "python", "python3":
		return model.LanguagePython
	default:
		return ""
	}
}
