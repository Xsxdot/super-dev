// handler_ssh_config.go 实现 GET /api/ssh-config/hosts:
// 解析 ~/.ssh/config 并返回主机条目列表,用于"从 SSH config 导入"快捷方法。
//
// 职责：
//   - 调用 sshconfig.ParseFile 读取并解析 ~/.ssh/config
//   - 文件不存在时返回空数组(不视为错误)
//
// 边界：
//   - 仅读取,不修改 ssh config
//   - 解析子集见 sshconfig 包说明
package api

import (
	"net/http"

	"github.com/superdev/agent/sshconfig"
)

// listSSHConfigHosts 处理 GET /api/ssh-config/hosts。
func (a *App) listSSHConfigHosts(w http.ResponseWriter, r *http.Request) {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hosts, err := sshconfig.ParseFile(path)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hosts == nil {
		hosts = []sshconfig.Host{}
	}
	jsonOK(w, hosts)
}
