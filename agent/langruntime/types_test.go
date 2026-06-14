// types_test.go 验证 Language Runtime Provider 的公共契约。
//
// 职责：锁定 RuntimeSchema 校验规则、诊断工具函数等基础行为。
// 边界：不验证具体语言 provider 的计划生成细节。
package langruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestRuntimeSchemaValidateRequiresKeyNameDesc(t *testing.T) {
	schema := langruntime.RuntimeSchema{
		Language: model.LanguageGo,
		Version:  1,
		Title:    langruntime.LocalizedText{Key: "runtime.go.title", Default: "Go"},
		Fields: []langruntime.RuntimeSchemaField{{
			Key:      "program",
			Name:     langruntime.LocalizedText{Key: "runtime.go.program.name", Default: "Go entry package"},
			Desc:     langruntime.LocalizedText{Key: "runtime.go.program.desc", Default: "Main package"},
			Type:     langruntime.FieldTypeString,
			Required: true,
		}},
	}
	require.NoError(t, schema.Validate())
}

func TestRuntimeSchemaValidateRejectsMissingDesc(t *testing.T) {
	schema := langruntime.RuntimeSchema{
		Language: model.LanguageGo,
		Version:  1,
		Title:    langruntime.LocalizedText{Key: "runtime.go.title", Default: "Go"},
		Fields: []langruntime.RuntimeSchemaField{{
			Key:  "program",
			Name: langruntime.LocalizedText{Key: "runtime.go.program.name", Default: "Go entry package"},
			Type: langruntime.FieldTypeString,
		}},
	}
	err := schema.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field program desc is required")
}

func TestRuntimeSchemaValidateRejectsUnsupportedFieldType(t *testing.T) {
	schema := langruntime.RuntimeSchema{
		Language: model.LanguageGo,
		Version:  1,
		Title:    langruntime.LocalizedText{Key: "runtime.go.title", Default: "Go"},
		Fields: []langruntime.RuntimeSchemaField{{
			Key:  "mode",
			Name: langruntime.LocalizedText{Key: "k", Default: "Mode"},
			Desc: langruntime.LocalizedText{Key: "k2", Default: "Mode"},
			Type: langruntime.FieldType("enum"),
		}},
	}
	err := schema.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field mode type enum is unsupported")
}
