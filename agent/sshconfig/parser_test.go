// Package sshconfig_test 验证 ~/.ssh/config 解析。
package sshconfig_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/sshconfig"
)

func TestParseBasic(t *testing.T) {
	content := `
Host compute-01
    HostName 10.0.0.1
    User ops
    Port 22
    IdentityFile ~/.ssh/id_ed25519

Host compute-02
    HostName 10.0.0.2
    User dev
    IdentityFile ~/.ssh/id_rsa

Host *.skip
    User wildcard
`
	hosts, err := sshconfig.Parse(strings.NewReader(content))
	require.NoError(t, err)
	assert.Len(t, hosts, 2, "通配符条目应被跳过")
	assert.Equal(t, "compute-01", hosts[0].Name)
	assert.Equal(t, "10.0.0.1", hosts[0].HostName)
	assert.Equal(t, 22, hosts[0].Port)
	assert.Equal(t, "ops", hosts[0].User)
	assert.Equal(t, "~/.ssh/id_ed25519", hosts[0].IdentityFile)

	assert.Equal(t, "compute-02", hosts[1].Name)
	assert.Equal(t, 22, hosts[1].Port, "Port 缺省应填 22")
}

func TestParseIgnoresComments(t *testing.T) {
	content := `# global comment
Host a
    HostName 1.1.1.1
    # inline comment
`
	hosts, err := sshconfig.Parse(strings.NewReader(content))
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	assert.Equal(t, "a", hosts[0].Name)
}
