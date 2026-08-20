// validate_test.go 覆盖 validate.go 中 deployment.ports 的校验规则：
// 范围必须落在 1-65535，去重后不得重复声明。
package configchange

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// TestValidatePortsRange 断言方式对齐本文件其他 Validate 用例
// （result.OK + errors 拼接后 Contains），覆盖越界、负数、重复、合法四类输入。
func TestValidatePortsRange(t *testing.T) {
	cases := []struct {
		name    string
		ports   []int
		wantErr string // 空串表示应通过校验
	}{
		{name: "zero", ports: []int{0}, wantErr: "deployment dep-worker-dev: 端口 0 超出 1-65535"},
		{name: "too large", ports: []int{65536}, wantErr: "deployment dep-worker-dev: 端口 65536 超出 1-65535"},
		{name: "negative", ports: []int{-1}, wantErr: "deployment dep-worker-dev: 端口 -1 超出 1-65535"},
		{name: "duplicate", ports: []int{9100, 9100}, wantErr: "deployment dep-worker-dev: 端口 9100 重复声明"},
		{name: "valid single port", ports: []int{9100}, wantErr: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := sampleProject()
			project.Services[0].Deployments[0].Ports = tc.ports

			result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

			if tc.wantErr == "" {
				require.True(t, result.OK, result.Errors)
				return
			}
			require.False(t, result.OK)
			assert.Contains(t, strings.Join(result.Errors, "\n"), tc.wantErr)
		})
	}
}

// TestValidateRemoteRuntimeTypeRejectsSelfLaunchedRuntimes 钉死
// 「远端 deployment 不能用 SuperDev 自己拉起进程的 runtime」。
//
// 为什么这条必须在校验层拦住而不是留给运行时报错：这个组合是可以一路走通到
// 目标机的——下发成功、合成项目成功、界面上看起来一切正常，只是状态永远
// stopped、端口镜像永远不建立。没有任何一步会报错，用户只能靠猜。
func TestValidateRemoteRuntimeTypeRejectsSelfLaunchedRuntimes(t *testing.T) {
	for _, rt := range []model.RuntimeType{model.RuntimeTypeCommand, model.RuntimeTypeLanguage} {
		t.Run(string(rt), func(t *testing.T) {
			dep := model.Deployment{
				ID:       "dep-1",
				EnvName:  "dev",
				Location: model.LocationRemote,
				HostIDs:  []string{"host-1"},
				Runtime:  &model.RuntimeConfig{Type: rt},
			}
			errs := validateRemoteRuntimeType("web", dep)
			require.Len(t, errs, 1)
			require.Contains(t, errs[0], string(rt))
			// 文案必须给出替代路径，否则用户只知道被拒、不知道该怎么办。
			require.Contains(t, errs[0], "归属")
		})
	}
}

// TestValidateRemoteRuntimeTypeAllowsSupervisorRuntimes 钉死基座型 runtime 不受影响。
//
// 为什么单列：这三种是路径 A 的本职形态（进程由 systemd/docker/launchd 接管，
// SuperDev 只采样），真机验收已验证可用。误伤它们等于把远端服务管理整个废掉。
func TestValidateRemoteRuntimeTypeAllowsSupervisorRuntimes(t *testing.T) {
	for _, rt := range []model.RuntimeType{model.RuntimeTypeSystemd, model.RuntimeTypeDocker, model.RuntimeTypeLaunchd} {
		dep := model.Deployment{
			ID: "dep-1", EnvName: "dev", Location: model.LocationRemote,
			HostIDs: []string{"host-1"}, Runtime: &model.RuntimeConfig{Type: rt},
		}
		require.Empty(t, validateRemoteRuntimeType("web", dep), "runtime %s 不应被拒", rt)
	}
}

// TestValidateRemoteRuntimeTypeIgnoresNonRemoteAndRuntimeless 钉死不越界。
//
// 本机 deployment 用 command/language 是最常见的正常配置；
// 远端不声明 runtime 则是「只采日志、不采样进程状态」的合法配法（日志采集
// 由 Logs 配置独立驱动），两者都不该被这条规则碰到。
func TestValidateRemoteRuntimeTypeIgnoresNonRemoteAndRuntimeless(t *testing.T) {
	local := model.Deployment{
		ID: "dep-1", EnvName: "dev", Location: model.LocationLocal,
		Runtime: &model.RuntimeConfig{Type: model.RuntimeTypeLanguage},
	}
	require.Empty(t, validateRemoteRuntimeType("web", local))

	remoteNoRuntime := model.Deployment{
		ID: "dep-2", EnvName: "dev", Location: model.LocationRemote, HostIDs: []string{"h"},
	}
	require.Empty(t, validateRemoteRuntimeType("web", remoteNoRuntime))
}
