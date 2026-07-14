// Package api serves bundled manual Agent uninstall scripts.
//
// Responsibilities:
//   - Expose the Shell and PowerShell scripts bundled with the running Controller.
//   - Attach the Controller version and stable download filenames to each response.
//   - Restrict file access to the two Agent-owned release assets.
//
// Boundaries:
//   - Does not generate scripts or fetch assets from the network.
//   - Does not run uninstall or mutate Host and Agent configuration.
package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/internal/buildinfo"
)

var agentUninstallScriptContentTypes = map[string]string{
	"uninstall-agent.sh":  "text/x-shellscript; charset=utf-8",
	"uninstall-agent.ps1": "text/plain; charset=utf-8",
}

// serveAgentUninstallScript handles GET /api/agents/uninstall-scripts/{name}.
//
// The route serves only scripts from InstallBinaryDir, which is populated from the same
// source revision as the packaged Controller. It never accepts an arbitrary filesystem path.
func (a *App) serveAgentUninstallScript(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	contentType, supported := agentUninstallScriptContentTypes[name]
	requestLog := logger.GetLogger().WithEntryName("AgentUninstallScript").WithFields(map[string]any{
		"asset":   name,
		"version": buildinfo.Version,
	})
	requestLog.Info("开始读取随 Controller 打包的 Agent 手动卸载脚本")
	if !supported || a.cfg.InstallBinaryDir == "" {
		requestLog.Error("请求的 Agent 手动卸载脚本不存在")
		jsonError(w, http.StatusNotFound, "manual uninstall script not found")
		return
	}

	scriptPath := filepath.Join(a.cfg.InstallBinaryDir, name)
	info, err := os.Stat(scriptPath)
	if err != nil {
		requestLog.WithErr(err).Error("读取 Agent 手动卸载脚本失败")
		jsonError(w, http.StatusNotFound, "manual uninstall script not found")
		return
	}
	if !info.Mode().IsRegular() {
		requestLog.WithField("mode", info.Mode().String()).Error("Agent 手动卸载脚本不是普通文件")
		jsonError(w, http.StatusNotFound, "manual uninstall script not found")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-SuperDev-Agent-Version", buildinfo.Version)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, scriptPath)
	requestLog.WithField("bytes", info.Size()).Info("Agent 手动卸载脚本已返回")
}
