package budget

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGovernor(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{"eng": {Daily: 1000}},
	}
	g := NewGovernor(s, cfg, slog.Default())
	assert.NotNil(t, g)
	assert.True(t, g.Enabled())
}

func TestNewGovernor_NilConfig(t *testing.T) {
	s := NewBudgetStore()
	g := NewGovernor(s, nil, slog.Default())
	assert.NotNil(t, g)
	assert.False(t, g.Enabled())
}

func TestNewGovernor_NilLogger(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{"eng": {Daily: 1000}},
	}
	g := NewGovernor(s, cfg, nil)
	assert.NotNil(t, g)
	assert.True(t, g.Enabled())
}

func TestGovernor_Deduct_NoBudget(t *testing.T) {
	s := NewBudgetStore()
	g := NewGovernor(s, nil, slog.Default())
	err := g.Deduct("engineering", "", 100)
	assert.NoError(t, err)
}

func TestGovernor_Deduct_UnknownTeam(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{"eng": {Daily: 1000}},
	}
	g := NewGovernor(s, cfg, slog.Default())
	err := g.Deduct("unknown", "", 100)
	assert.NoError(t, err)
}

func TestGovernor_Deduct_TeamOnly(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{"engineering": {Daily: 100000}},
	}
	g := NewGovernor(s, cfg, slog.Default())

	err := g.Deduct("engineering", "", 30000)
	assert.NoError(t, err)
	assert.Equal(t, 70000, s.TeamDailyRemaining("engineering"))
}

func TestGovernor_Deduct_TeamExceeded(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{"engineering": {Daily: 100}},
	}
	g := NewGovernor(s, cfg, slog.Default())

	err := g.Deduct("engineering", "", 200)
	assert.Error(t, err)
}

func TestGovernor_Deduct_WithProject(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{
			"engineering": {
				Daily: 100000,
				Projects: map[string]ProjectBudget{
					"backend": {Monthly: 500000},
				},
			},
		},
	}
	g := NewGovernor(s, cfg, slog.Default())

	err := g.Deduct("engineering", "backend", 30000)
	assert.NoError(t, err)
	assert.Equal(t, int64(30000), s.ProjectMonthlyUsed("engineering", "backend"))
}

func TestGovernor_Deduct_ProjectExceeded(t *testing.T) {
	s := NewBudgetStore()
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{
			"engineering": {
				Daily: 100000,
				Projects: map[string]ProjectBudget{
					"backend": {Monthly: 500},
				},
			},
		},
	}
	g := NewGovernor(s, cfg, slog.Default())

	err := g.Deduct("engineering", "backend", 600)
	assert.Error(t, err)
}
