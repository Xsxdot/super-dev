package collector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

func TestSystemProbeUnknownType(t *testing.T) {
	probe := collector.NewSystemProbe()
	err := probe.Exists(model.LogSourceType("kubectl"), "foo")
	assert.ErrorIs(t, err, collector.ErrUnsupportedType)
}

// 真实 systemctl/docker 调用依赖运行环境,放在 _linux_test.go 用 build tag 隔离。
// 本测试只覆盖错误分支,确保接口稳定。
func TestSystemProbeInvalidName(t *testing.T) {
	probe := collector.NewSystemProbe()
	err := probe.Exists(model.LogSourceTypeJournalctl, "; rm -rf /")
	assert.ErrorIs(t, err, collector.ErrInvalidName)
}
