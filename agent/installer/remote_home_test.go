package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswdHomeFieldTakesSixthField(t *testing.T) {
	home, ok := passwdHomeField("alice:x:1000:1000:Alice:/opt/app:/bin/bash\n")
	require.True(t, ok)
	assert.Equal(t, "/opt/app", home)
}

func TestPasswdHomeFieldRejectsJunkWithoutPanic(t *testing.T) {
	cases := []string{"", "nocolon", "a:b:c", ":::::", "alice:x:1000:1000:Alice::/bin/bash", "alice:x:1000:1000:Alice:relative:/bin/bash"}
	for _, raw := range cases {
		home, ok := passwdHomeField(raw)
		assert.False(t, ok, "raw=%q", raw)
		assert.Empty(t, home)
	}
}

func TestEvalTildeHomeCommandDoesNotQuoteUser(t *testing.T) {
	assert.Equal(t, "eval echo ~alice", evalTildeHomeCommand("alice"))
	assert.Equal(t, "getent passwd 'alice'", getentPasswdCommand("alice"))
}
