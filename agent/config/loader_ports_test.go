// loader_ports_test.go 验证 deployment 声明端口（ports）在 project.yaml
// （split 格式共享层）的读写 round-trip：Load 解析出 ports，Save 后重新
// Load，ports 原样保留，不丢失、不被静默改写。
package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
)

func TestDeploymentPortsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mustWriteSuperdev(t, dir, "project.yaml", `
name: demo
services:
  - name: api
    deployments:
      - env: dev
        location: local
        ports: [9100, 9101]
`)

	loader := config.NewLoader(dir)
	p, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, p.Services, 1)
	require.Len(t, p.Services[0].Deployments, 1)
	assert.Equal(t, []int{9100, 9101}, p.Services[0].Deployments[0].Ports)

	// save 后重读，ports 不丢失（save 保持声明字段原样，不因往返而漂移）
	require.NoError(t, loader.Save(p))
	reloaded, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, []int{9100, 9101}, reloaded.Services[0].Deployments[0].Ports)
}
