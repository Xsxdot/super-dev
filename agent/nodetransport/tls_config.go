// Package nodetransport 提供 Agent 级 TLS 配置到客户端配置的转换。
//
// 职责：
//   - 将 AgentSecurity.TLS 映射为 HTTP/WebSocket 客户端 TLS 配置
//   - 供 direct 和 tunnel transport 共享统一 TLS 语义
//
// 边界：
//   - 不决定 TLS 配置如何生成或下发
//   - 不读取证书文件，CACert 必须是已持久化的 PEM 内容
package nodetransport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/model"
)

func httpClientForAgentTLS(spec model.AgentTLSSpec, connectTimeout, requestTimeout time.Duration) (*http.Client, error) {
	tlsConfig, err := tlsConfigForAgentTLS(spec)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   connectTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: connectTimeout,
			TLSClientConfig:     tlsConfig,
		},
	}, nil
}

func wsDialerForAgentTLS(spec model.AgentTLSSpec, connectTimeout time.Duration) (*websocket.Dialer, error) {
	tlsConfig, err := tlsConfigForAgentTLS(spec)
	if err != nil {
		return nil, err
	}
	return &websocket.Dialer{
		HandshakeTimeout: connectTimeout,
		TLSClientConfig:  tlsConfig,
		NetDialContext: (&net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}, nil
}

func tlsConfigForAgentTLS(spec model.AgentTLSSpec) (*tls.Config, error) {
	if !agentTLSEnabled(spec) {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(spec.ServerName) != "" {
		cfg.ServerName = strings.TrimSpace(spec.ServerName)
	}
	if strings.TrimSpace(spec.CACert) == "" {
		return cfg, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(spec.CACert)) {
		return nil, fmt.Errorf("invalid agent CA certificate")
	}
	cfg.RootCAs = pool
	return cfg, nil
}

func agentTLSEnabled(spec model.AgentTLSSpec) bool {
	return spec.Mode != model.AgentTLSModeOff
}

func tlsSpecForRequest(agent model.Agent, req NodeRequest) model.AgentTLSSpec {
	if req.TLSOverride != nil {
		return *req.TLSOverride
	}
	return agent.Security.TLS
}
