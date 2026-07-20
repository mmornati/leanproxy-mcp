package budget

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBudgetAction_String(t *testing.T) {
	assert.Equal(t, "allow", BudgetActionAllow.String())
	assert.Equal(t, "downgrade", BudgetActionDowngrade.String())
	assert.Equal(t, "reject", BudgetActionReject.String())
	assert.Equal(t, "unknown", BudgetAction(99).String())
}

func TestBudgetExceededError_Error(t *testing.T) {
	t.Run("hard cap", func(t *testing.T) {
		err := &BudgetExceededError{Team: "eng", HardCap: true}
		assert.Contains(t, err.Error(), "hard cap")
		assert.Contains(t, err.Error(), "eng")
	})

	t.Run("daily and monthly", func(t *testing.T) {
		err := &BudgetExceededError{Team: "eng", Daily: true, Monthly: true}
		assert.Contains(t, err.Error(), "daily")
		assert.Contains(t, err.Error(), "monthly")
	})

	t.Run("daily only", func(t *testing.T) {
		err := &BudgetExceededError{Team: "eng", Daily: true}
		assert.Contains(t, err.Error(), "daily")
	})

	t.Run("monthly only", func(t *testing.T) {
		err := &BudgetExceededError{Team: "eng", Monthly: true}
		assert.Contains(t, err.Error(), "monthly")
	})
}

func ptr[V any](v V) *V { return &v }

func TestTeamBudget_SoftCapPercentage(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		tb := &TeamBudget{Daily: 1000}
		assert.Equal(t, 90.0, tb.SoftCapPercentage())
	})

	t.Run("custom", func(t *testing.T) {
		tb := &TeamBudget{Daily: 1000, SoftCapPct: ptr(80.0)}
		assert.Equal(t, 80.0, tb.SoftCapPercentage())
	})

	t.Run("zero", func(t *testing.T) {
		tb := &TeamBudget{Daily: 1000, SoftCapPct: ptr(0.0)}
		assert.Equal(t, 0.0, tb.SoftCapPercentage())
	})
}

func TestEvaluateBudget_Disabled(t *testing.T) {
	store := NewBudgetStore()
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_NilConfig(t *testing.T) {
	store := NewBudgetStore()
	dec := EvaluateBudget("eng", "", store, &BudgetConfig{Teams: nil}, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_UnknownTeam(t *testing.T) {
	store := NewBudgetStore()
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"known": {Daily: 1000}}}
	dec := EvaluateBudget("unknown", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_Allow(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 100, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
	assert.InDelta(t, 10.0, dec.DailyPct, 0.01)
}

func TestEvaluateBudget_IgnoreBudget(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 1000, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}
	dec := EvaluateBudget("eng", "", store, cfg, true, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_DowngradeAtThreshold(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 900, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000, SoftCapPct: ptr(80.0)}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionDowngrade, dec.Action)
	assert.InDelta(t, 90.0, dec.DailyPct, 0.01)
}

func TestEvaluateBudget_RejectDailyExceeded(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 1000, 1000)
	assert.Equal(t, 0, store.TeamDailyRemaining("eng"))
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionReject, dec.Action)
	assert.InDelta(t, 100.0, dec.DailyPct, 0.01)
}

func TestEvaluateBudget_RejectHardCap(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 1000, 1000)
	assert.Equal(t, 0, store.TeamDailyRemaining("eng"))
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000, HardCap: true}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionReject, dec.Action)
	assert.Contains(t, dec.Message, "hard cap")
}

func TestEvaluateBudget_HardCapAtThreshold(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 900, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000, HardCap: true, SoftCapPct: ptr(80.0)}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionReject, dec.Action)
	assert.Contains(t, dec.Message, "hard cap")
	assert.InDelta(t, 90.0, dec.DailyPct, 0.01)
}

func TestEvaluateBudget_DefaultSoftCapPct(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 900, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionDowngrade, dec.Action)
}

func TestEvaluateBudget_BelowSoftCap(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 100, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_NoMonthlyLimit(t *testing.T) {
	store := NewBudgetStore()
	store.DeductTeam("eng", 100, 1000)
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 1000, Monthly: 0}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionAllow, dec.Action)
}

func TestEvaluateBudget_MonthlyExceeded(t *testing.T) {
	store := NewBudgetStore()
	tu := store.EnsureTeam("eng", 100000)
	tu.mu.Lock()
	tu.monthlyUsed = 40000
	tu.mu.Unlock()
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 100000, Monthly: 40000}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionReject, dec.Action)
	assert.InDelta(t, 100.0, dec.MonthlyPct, 0.01)
}

func TestEvaluateBudget_MonthlyAtThreshold(t *testing.T) {
	store := NewBudgetStore()
	tu := store.EnsureTeam("eng", 100000)
	tu.mu.Lock()
	tu.monthlyUsed = 36000
	tu.dailyBucket.AddN(100000)
	tu.mu.Unlock()
	cfg := &BudgetConfig{Teams: map[string]TeamBudget{"eng": {Daily: 100000, Monthly: 40000, SoftCapPct: ptr(80.0)}}}
	dec := EvaluateBudget("eng", "", store, cfg, false, slog.Default())
	assert.Equal(t, BudgetActionDowngrade, dec.Action)
	assert.InDelta(t, 90.0, dec.MonthlyPct, 0.01)
}