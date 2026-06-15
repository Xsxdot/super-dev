// portalloc.go 分配本机空闲 TCP 端口，供 prearm-listen 语言（Python debugpy --listen）使用。
//
// 职责：让内核选一个空闲端口并立即释放，返回端口号供进程绑定。
// 边界：存在 TOCTOU 窗口（释放到再次绑定之间端口可能被抢），调用方应容忍绑定失败重试。
package codedebug

import "net"

// AllocateFreePort 让内核分配一个空闲端口后立即关闭监听，返回该端口号。
func AllocateFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
