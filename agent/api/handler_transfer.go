// handler_transfer.go 实现远端 agent 文件传输接口。
//
// 职责：
//   - 接收 multipart 文件流
//   - 委托 remoteexec.SaveMultipartTransfer 写入目标路径
//   - 返回明确 HTTP 状态
//
// 边界：
//   - 不解析 pipeline step
//   - 不做 SSH fallback
//   - 不展开目录，目录 source 由调用端先打成 tar.gz
package api

import (
	"net/http"

	"github.com/xsxdot/super-dev/agent/remoteexec"
)

// transferFile 处理 POST /api/transfer，把 multipart 文件写到目标路径。
func (a *App) transferFile(w http.ResponseWriter, r *http.Request) {
	if _, err := remoteexec.SaveMultipartTransfer(r); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
