// handler_hosts_scan_test.go 验证 POST /api/hosts/scan-host-key 只读采集接口。
//
// 职责：
//   - 验证成功路径返回与测试 SSH server 一致的 fingerprint
//   - 验证不可达目标返回 502 + ssh_host_unreachable
//   - 验证缺少 ssh_host 返回 400
//   - 验证采集接口绝不写入 host 存储（只读边界）
//
// 边界：
//   - 复用 api_test 包已有的 newTestApp 与真实 httptest.Server，不引入新的测试 helper
package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/testsupport/sshtest"
)

// TestScanHostKeyReturnsFingerprint 验证成功采集返回的 fingerprint 与测试 server 一致。
func TestScanHostKeyReturnsFingerprint(t *testing.T) {
	srv, _ := newTestApp(t)
	server := sshtest.Start(t)
	host, portStr, err := net.SplitHostPort(server.Address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"ssh_host": host, "ssh_port": port})
	resp, err := http.Post(srv.URL+"/api/hosts/scan-host-key", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var payload struct {
		Fingerprint string `json:"fingerprint"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	assert.Equal(t, server.Fingerprint, payload.Fingerprint)
}

// TestScanHostKeyUnreachableReturnsCode 验证目标不可达时返回 502 + 稳定错误码。
func TestScanHostKeyUnreachableReturnsCode(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(map[string]any{"ssh_host": "127.0.0.1", "ssh_port": 1})
	resp, err := http.Post(srv.URL+"/api/hosts/scan-host-key", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)

	var payload struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	assert.Equal(t, "ssh_host_unreachable", payload.Code)
}

// TestScanHostKeyRejectsEmptyHost 验证缺少 ssh_host 时返回 400。
func TestScanHostKeyRejectsEmptyHost(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(map[string]any{"ssh_host": "", "ssh_port": 22})
	resp, err := http.Post(srv.URL+"/api/hosts/scan-host-key", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestScanHostKeyDoesNotPersistHost 验证采集接口只读，绝不写入 host 存储。
func TestScanHostKeyDoesNotPersistHost(t *testing.T) {
	srv, _ := newTestApp(t)
	server := sshtest.Start(t)
	host, portStr, err := net.SplitHostPort(server.Address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	beforeResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	beforeBody, err := io.ReadAll(beforeResp.Body)
	_ = beforeResp.Body.Close()
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"ssh_host": host, "ssh_port": port})
	scanResp, err := http.Post(srv.URL+"/api/hosts/scan-host-key", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_, err = io.ReadAll(scanResp.Body)
	_ = scanResp.Body.Close()
	require.NoError(t, err)

	afterResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	afterBody, err := io.ReadAll(afterResp.Body)
	_ = afterResp.Body.Close()
	require.NoError(t, err)

	assert.Equal(t, string(beforeBody), string(afterBody), "scan must not mutate host store")
}
