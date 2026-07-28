// handler_ssh_keys.go 实现 GET /api/ssh-keys:
// 扫描本机 ~/.ssh 并返回私钥候选列表，用于「一键导入本机私钥」。
//
// 职责：
//   - 调用 sshkeys.Scan 列出候选，目录不存在时返回空数组
//
// 边界：
//   - 只读，不返回私钥内容——响应仅含路径与元信息
//   - 私钥内容在保存 Host 时由 importHostPrivateKey 在 agent 侧读取
package api

import (
	"net/http"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/sshkeys"
)

// listSSHKeys 处理 GET /api/ssh-keys。
func (a *App) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("SSHKeys").WithField("operation", "list")

	dir, err := sshkeys.DefaultDir()
	if err != nil {
		log.Errorf("定位 SSH 密钥目录失败: %v", err)
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keys, err := sshkeys.Scan(dir)
	if err != nil {
		log.Errorf("扫描 SSH 密钥失败: %v", err)
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if keys == nil {
		keys = []sshkeys.Key{}
	}
	log.Infof("返回 SSH 私钥候选 %d 个", len(keys))
	jsonOK(w, keys)
}
