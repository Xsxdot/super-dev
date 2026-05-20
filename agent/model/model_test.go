package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/model"
)

func TestServiceDefaults(t *testing.T) {
	s := model.Service{Name: "web", Command: "go run ."}
	assert.Equal(t, 0, s.Order)
	assert.False(t, s.Required)
	assert.Equal(t, model.StatusStopped, s.Status)
}

func TestProjectSelectedIDs(t *testing.T) {
	p := model.Project{Name: "myapp"}
	assert.Empty(t, p.SelectedServiceIDs)
}

func TestLogRuleTypes(t *testing.T) {
	r := model.LogRule{Type: model.RuleTypeExclude, Logic: model.RuleLogicOR}
	assert.Equal(t, "exclude", string(r.Type))
	assert.Equal(t, "or", string(r.Logic))
}
