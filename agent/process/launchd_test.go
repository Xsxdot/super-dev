package process

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestLaunchdTargetUsesGUIUserDomain(t *testing.T) {
	target, err := launchdTarget(501, "com.example.api")
	require.NoError(t, err)
	assert.Equal(t, "gui/501/com.example.api", target)
}

func TestLaunchdTargetRequiresLabel(t *testing.T) {
	_, err := launchdTarget(501, " ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "launchd label")
}

func TestLaunchdCommands(t *testing.T) {
	dep := model.Deployment{
		Runtime: &model.RuntimeConfig{
			Type:      model.RuntimeTypeLaunchd,
			Label:     "com.example.api",
			PlistPath: "/Users/me/Library/LaunchAgents/com.example.api.plist",
		},
	}

	bootstrap, kickstart, err := launchdStartCommands(501, dep)
	require.NoError(t, err)
	assert.Equal(t, []launchdCommand{
		{name: "launchctl", args: []string{"bootstrap", "gui/501", "/Users/me/Library/LaunchAgents/com.example.api.plist"}},
	}, bootstrap)
	assert.Equal(t, launchdCommand{name: "launchctl", args: []string{"kickstart", "-k", "gui/501/com.example.api"}}, kickstart)

	stop, err := launchdStopCommand(501, dep)
	require.NoError(t, err)
	assert.Equal(t, launchdCommand{name: "launchctl", args: []string{"bootout", "gui/501/com.example.api"}}, stop)
}
