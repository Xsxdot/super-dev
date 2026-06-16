// registry_test.go 验证 Language Runtime Provider 注册表。
//
// 职责：锁定内置 provider 注册、按语言查找以及语言列表稳定排序。
// 边界：不验证 provider 内部 schema 或执行计划。
package langruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestCoreRegistryHasGoProvider(t *testing.T) {
	provider, ok := langruntime.Core().Provider(model.LanguageGo)
	require.True(t, ok)
	assert.Equal(t, model.LanguageGo, provider.Language())
	// Go 的 debugger-ready 策略是 attach：start_dev 不附加任何调试预埋
	assert.Equal(t, langruntime.DebugReadyByAttach, provider.Capabilities().DebugReady)
}

func TestCoreRegistersNode(t *testing.T) {
	_, ok := langruntime.Core().Provider(model.LanguageNode)
	assert.True(t, ok)
}

func TestCoreRegistersPython(t *testing.T) {
	_, ok := langruntime.Core().Provider(model.LanguagePython)
	assert.True(t, ok)
}

func TestCoreRegistersAllLanguages(t *testing.T) {
	for _, language := range []model.ServiceLanguage{
		model.LanguageGo,
		model.LanguageNode,
		model.LanguagePython,
		model.LanguageJava,
		model.LanguageKotlin,
		model.LanguageRust,
		model.LanguageCpp,
	} {
		if _, ok := langruntime.Core().Provider(language); !ok {
			t.Fatalf("language %s not registered", language)
		}
	}
}

func TestRegistryListsLanguagesInStableOrder(t *testing.T) {
	reg := langruntime.NewRegistry()
	reg.Register(langruntime.NewGoProvider())
	langs := reg.Languages()
	assert.Equal(t, []model.ServiceLanguage{model.LanguageGo}, langs)
}
