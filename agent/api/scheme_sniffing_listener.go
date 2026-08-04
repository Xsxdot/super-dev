// scheme_sniffing_listener.go —— TLS 姿态下的同端口协议嗅探 listener。
//
// 职责：
//   - 按连接首字节区分 TLS 与明文 HTTP：0x16（TLS ClientHello 记录头）走 TLS
//     握手，其余按明文处理
//   - 明文连接仅当 TCP 对端为 loopback 时放行（本机明文豁免）；非 loopback
//     明文回一条纯文本 400 后关闭——与 Go 标准库 TLS 监听器对明文请求的
//     行为对齐，让误用 http:// 的远端客户端拿到可读的错误而非静默断连
//
// 边界：
//   - 只做协议分流，不做任何鉴权判定（鉴权始终由 withSecurity 中间件承担，
//     明文豁免不放松任何凭据要求）
//   - 明文豁免以 TCP 连接对端地址为准：跨机流量无法伪造 loopback 源地址，
//     故跨机链路的 TLS 强制姿态不受影响；经 SSH 端口转发/port mirror 抵达
//     loopback 的流量视同本机流量（发起方已具备本机接入权，且链路自带加密）
package api

import (
	"bytes"
	"crypto/tls"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// sniffReadTimeout 是等待连接首字节的上限。正常客户端（TLS ClientHello 或
// HTTP 请求行）连接后立即发送首字节，5s 足够覆盖慢网络；超时连接直接关闭，
// 避免恶意/异常连接长期占用嗅探 goroutine。
const sniffReadTimeout = 5 * time.Second

// tlsRecordTypeHandshake 是 TLS record 层 Handshake 类型的首字节值。
// 任何合法 HTTP 方法都不会以 0x16 开头，单字节即可无歧义分流。
const tlsRecordTypeHandshake = 0x16

// plaintextRejectResponse 是拒绝非 loopback 明文连接时写回的响应。
// 措辞对齐 Go 标准库 TLS 监听器对明文请求的回复，便于调用方按同类错误识别。
const plaintextRejectResponse = "HTTP/1.0 400 Bad Request\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n" +
	"Client sent an HTTP request to an HTTPS server.\n"

// schemeSniffingListener 在单个 TCP 端口上同时服务 TLS 与本机明文流量。
//
// Accept 返回的连接已完成协议分流：TLS 连接是 *tls.Conn（http.Server 据此
// 填充 r.TLS），明文连接是带回放首字节的普通连接。嗅探在独立 goroutine 中
// 进行，慢客户端不会阻塞其他连接的 Accept。
type schemeSniffingListener struct {
	inner     net.Listener
	tlsConfig *tls.Config
	conns     chan net.Conn
	errs      chan error
	closeOnce sync.Once
	done      chan struct{}
}

// newSchemeSniffingListener 包装 ln，按首字节分流 TLS 与 loopback 明文连接。
//
// 参数：
//   - ln: 原始 TCP listener
//   - tlsConfig: TLS 握手配置（来自 tlsConfigForListen，含服务端证书）
//
// 返回：
//   - 可直接交给 http.Server.Serve 的复合 listener
//
// 注意：会 clone tlsConfig 并补齐 NextProtos（h2/http1.1）——手工 tls.Server
// 包装不会像 ListenAndServeTLS 那样自动注入 ALPN，不补齐会静默失去 HTTP/2。
func newSchemeSniffingListener(ln net.Listener, tlsConfig *tls.Config) *schemeSniffingListener {
	cfg := tlsConfig.Clone()
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{"h2", "http/1.1"}
	}
	l := &schemeSniffingListener{
		inner:     ln,
		tlsConfig: cfg,
		conns:     make(chan net.Conn),
		errs:      make(chan error, 1),
		done:      make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

// acceptLoop 持续接收原始连接并逐个交由嗅探 goroutine 分流。
// inner listener 关闭后 Accept 返回错误，错误经 errs 通道转交上层的
// http.Server.Serve，使其按正常关闭路径退出。
func (l *schemeSniffingListener) acceptLoop() {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			select {
			case l.errs <- err:
			case <-l.done:
			}
			return
		}
		go l.sniff(conn)
	}
}

// sniff 读取首字节完成协议分流，并把结果连接投递给 Accept。
//
// 注意：
//   - 读不到首字节（对端连接即关/超时）直接静默关闭：桌面端端口占用检查、
//     健康探活等会产生大量 connect-then-close 连接，逐条打日志只会刷屏
//   - 非 loopback 明文是关键诊断分支（远端客户端误用 http://），必须打日志
func (l *schemeSniffingListener) sniff(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(sniffReadTimeout))
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	// 首字节已被消费，用 prefixConn 回放，保证后续 TLS 握手/HTTP 解析读到完整流。
	pc := &prefixConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(first[:]), conn)}
	if first[0] == tlsRecordTypeHandshake {
		l.deliver(tls.Server(pc, l.tlsConfig))
		return
	}
	if !isLoopbackAddr(conn.RemoteAddr()) {
		// 远端明文一律拒绝：TLS 姿态对跨机流量不打折。回可读 400 帮对端定位。
		log.Printf("[SuperDev] api: 拒绝非 loopback 明文连接（TLS 姿态开启，请改用 https://） remote=%s", conn.RemoteAddr())
		_ = conn.SetWriteDeadline(time.Now().Add(sniffReadTimeout))
		_, _ = conn.Write([]byte(plaintextRejectResponse))
		conn.Close()
		return
	}
	// loopback 明文豁免：本机客户端（superdev-mcp/桌面端）无需处理自签证书。
	// 不逐连接打日志——本机高频流量会刷屏，豁免生效与否由 Serve 的启动日志标识。
	l.deliver(pc)
}

// deliver 把分流完成的连接投递给 Accept；listener 已关闭时直接关掉连接。
func (l *schemeSniffingListener) deliver(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-l.done:
		conn.Close()
	}
}

// Accept 返回下一个已完成协议分流的连接。
func (l *schemeSniffingListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close 关闭底层 listener 并唤醒所有阻塞中的投递/Accept。
func (l *schemeSniffingListener) Close() error {
	err := l.inner.Close()
	l.closeOnce.Do(func() { close(l.done) })
	return err
}

// Addr 返回底层 listener 的监听地址。
func (l *schemeSniffingListener) Addr() net.Addr {
	return l.inner.Addr()
}

// isLoopbackAddr 判断 TCP 对端地址是否为 loopback。
// 解析失败按非 loopback 处理——与 isLoopbackRequest 同一保守纪律：
// 宁可错拒明文（对端还能改走 TLS），不可错放。
func isLoopbackAddr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// prefixConn 把已被嗅探消费的前缀字节拼回连接读流。
// 只有 Read 被改写；写路径与关闭语义直接透传底层连接。
type prefixConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
