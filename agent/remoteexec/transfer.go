// transfer.go 实现远端 agent 的 multipart 文件落盘能力。
//
// 职责：
//   - 从 multipart/form-data 读取 target 和 file
//   - 创建目标父目录
//   - 将上传内容写入目标路径
//
// 边界：
//   - 不解析 pipeline step
//   - 不建立隧道或 SSH 连接
//   - 不解包 tar.gz，目录包由调用端决定
package remoteexec

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// SaveMultipartTransfer 接收 multipart 文件并落盘到 target。
//
// 参数：
//   - r: multipart/form-data HTTP 请求，字段 target 为目标路径，字段 file 为文件内容
//
// 返回：
//   - 写入字节数
//   - 参数缺失、创建目录、创建文件或复制失败时返回错误
//
// 注意：
//   - 目标路径由调用方通过隧道传入，当前信任模型与现有远端 agent 接口一致
//   - 若 target 的父目录不存在，会自动创建，便于部署模板直接指定完整目标文件
func SaveMultipartTransfer(r *http.Request) (int64, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return 0, err
	}
	target := r.FormValue("target")
	if target == "" {
		return 0, errors.New("target is required")
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(target)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}
