package budget

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBudgetConfig_Enabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *BudgetConfig
		want bool
	}{
		{"nil config", nil, false},
		{"empty teams", &BudgetConfig{Teams: map[string]TeamBudget{}}, false},
		{"with teams", &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.Enabled())
		})
	}
}

func TestBudgetConfig_Team(t *testing.T) {
	cfg := &BudgetConfig{
		Teams: map[string]TeamBudget{
			"engineering": {Daily: 100000},
		},
	}

	t.Run("existing team", func(t *testing.T) {
		team := cfg.Team("engineering")
		assert.NotNil(t, team)
		assert.Equal(t, int64(100000), team.Daily)
	})

	t.Run("missing team", func(t *testing.T) {
		assert.Nil(t, cfg.Team("nonexistent"))
	})

	t.Run("nil config", func(t *testing.T) {
		var nilCfg *BudgetConfig
		assert.Nil(t, nilCfg.Team("anything"))
	})
}

func TestBudgetConfig_ProjectBudget(t *testing.T) {
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

	team := cfg.Team("engineering")
	assert.NotNil(t, team)

	proj, ok := team.Projects["backend"]
	assert.True(t, ok)
	assert.Equal(t, int64(500000), proj.Monthly)
}

func TestBudgetAlert_Interface(t *testing.T) {
	a := BudgetAlert{
		Team:       "eng",
		Project:    "web",
		Metric:     "monthly",
		Usage:      400000,
		Limit:      500000,
		Percentage: 80.0,
	}

	assert.Equal(t, "eng", a.TeamName())
	assert.Equal(t, "web", a.ProjectName())
	assert.Equal(t, "monthly", a.MetricName())
	assert.Equal(t, int64(400000), a.UsageAmount())
	assert.Equal(t, int64(500000), a.LimitAmount())
	assert.Equal(t, 80.0, a.PercentageValue())
}
