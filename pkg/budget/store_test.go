package budget

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewBudgetStore(t *testing.T) {
	s := NewBudgetStore()
	assert.NotNil(t, s)
}

func TestBudgetStore_EnsureTeam(t *testing.T) {
	s := NewBudgetStore()
	tu := s.EnsureTeam("engineering", 100000)
	assert.NotNil(t, tu)
	assert.Equal(t, 100000, tu.dailyBucket.Remaining())
}

func TestBudgetStore_EnsureTeam_Dedup(t *testing.T) {
	s := NewBudgetStore()
	tu1 := s.EnsureTeam("engineering", 100000)
	tu2 := s.EnsureTeam("engineering", 99999)
	assert.Equal(t, tu1, tu2)
	assert.Equal(t, 100000, tu2.dailyBucket.Remaining())
}

func TestBudgetStore_DeductTeam_Success(t *testing.T) {
	s := NewBudgetStore()
	remaining, err := s.DeductTeam("engineering", 30000, 100000)
	assert.NoError(t, err)
	assert.Equal(t, 70000, remaining)
}

func TestBudgetStore_DeductTeam_Exceeded(t *testing.T) {
	s := NewBudgetStore()
	_, err := s.DeductTeam("engineering", 100001, 100000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daily budget exceeded")
}

func TestBudgetStore_DeductTeam_Multiple(t *testing.T) {
	s := NewBudgetStore()
	s.DeductTeam("engineering", 40000, 100000)
	remaining, err := s.DeductTeam("engineering", 35000, 100000)
	assert.NoError(t, err)
	assert.Equal(t, 25000, remaining)
}

func TestBudgetStore_EnsureProject(t *testing.T) {
	s := NewBudgetStore()
	pu := s.EnsureProject("engineering", "backend")
	assert.NotNil(t, pu)
}

func TestBudgetStore_EnsureProject_Dedup(t *testing.T) {
	s := NewBudgetStore()
	p1 := s.EnsureProject("engineering", "backend")
	p2 := s.EnsureProject("engineering", "backend")
	assert.Equal(t, p1, p2)
}

func TestBudgetStore_DeductProject_Success(t *testing.T) {
	s := NewBudgetStore()
	used, err := s.DeductProject("engineering", "backend", 100000, 500000)
	assert.NoError(t, err)
	assert.Equal(t, int64(100000), used)
}

func TestBudgetStore_DeductProject_Exceeded(t *testing.T) {
	s := NewBudgetStore()
	_, err := s.DeductProject("engineering", "backend", 600000, 500000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "monthly budget exceeded")
}

func TestBudgetStore_DeductProject_NoLimit(t *testing.T) {
	s := NewBudgetStore()
	used, err := s.DeductProject("engineering", "backend", 600000, 0)
	assert.NoError(t, err)
	assert.Equal(t, int64(600000), used)
}

func TestBudgetStore_TeamDailyRemaining(t *testing.T) {
	s := NewBudgetStore()
	s.DeductTeam("engineering", 30000, 100000)
	assert.Equal(t, 70000, s.TeamDailyRemaining("engineering"))
}

func TestBudgetStore_TeamDailyRemaining_Unknown(t *testing.T) {
	s := NewBudgetStore()
	assert.Equal(t, 0, s.TeamDailyRemaining("nonexistent"))
}

func TestBudgetStore_TeamMonthlyUsed(t *testing.T) {
	s := NewBudgetStore()
	s.DeductTeam("engineering", 30000, 100000)
	s.DeductTeam("engineering", 20000, 100000)
	assert.Equal(t, int64(50000), s.TeamMonthlyUsed("engineering"))
}

func TestBudgetStore_TeamMonthlyUsed_Unknown(t *testing.T) {
	s := NewBudgetStore()
	assert.Equal(t, int64(0), s.TeamMonthlyUsed("nonexistent"))
}

func TestBudgetStore_ProjectMonthlyUsed(t *testing.T) {
	s := NewBudgetStore()
	s.DeductProject("engineering", "backend", 100000, 500000)
	s.DeductProject("engineering", "backend", 50000, 500000)
	assert.Equal(t, int64(150000), s.ProjectMonthlyUsed("engineering", "backend"))
}

func TestBudgetStore_ProjectMonthlyUsed_Unknown(t *testing.T) {
	s := NewBudgetStore()
	assert.Equal(t, int64(0), s.ProjectMonthlyUsed("eng", "unknown"))
}

func TestBudgetStore_DeductTeam_MonthlyReset(t *testing.T) {
	s := NewBudgetStore()
	tu := s.EnsureTeam("engineering", 1000)
	tu.resetMonth = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	tu.monthlyUsed = 50000

	remaining, err := s.DeductTeam("engineering", 100, 1000)
	assert.NoError(t, err)
	assert.Equal(t, 900, remaining)
	assert.Equal(t, int64(100), s.TeamMonthlyUsed("engineering"))
}

func TestBudgetStore_DeductProject_MonthlyReset(t *testing.T) {
	s := NewBudgetStore()
	pu := s.EnsureProject("engineering", "backend")
	pu.resetMonth = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	pu.monthlyUsed = 50000

	used, err := s.DeductProject("engineering", "backend", 10000, 100000)
	assert.NoError(t, err)
	assert.Equal(t, int64(10000), used)
}

func TestBudgetStore_DeductTeam_DailyReset(t *testing.T) {
	s := NewBudgetStore()
	tu := s.EnsureTeam("engineering", 1000)
	tu.resetDay = time.Now().Add(-48 * time.Hour)
	tu.resetMonth = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	tu.monthlyUsed = 50000

	remaining, err := s.DeductTeam("engineering", 100, 1000)
	assert.NoError(t, err)
	assert.Equal(t, 900, remaining)
	assert.Equal(t, int64(100), s.TeamMonthlyUsed("engineering"))
}

func TestBudgetStore_CheckProjectThreshold_Below(t *testing.T) {
	s := NewBudgetStore()
	s.DeductProject("engineering", "backend", 100000, 500000)

	var called bool
	s.CheckProjectThreshold("engineering", "backend", 500000, 80.0, func(a BudgetAlert) {
		called = true
	}, slog.Default())
	assert.False(t, called)
}

func TestBudgetStore_CheckProjectThreshold_Above(t *testing.T) {
	s := NewBudgetStore()
	s.DeductProject("engineering", "backend", 450000, 500000)

	var alert BudgetAlert
	s.CheckProjectThreshold("engineering", "backend", 500000, 80.0, func(a BudgetAlert) {
		alert = a
	}, slog.Default())
	assert.Equal(t, "engineering", alert.Team)
	assert.Equal(t, "backend", alert.Project)
	assert.Equal(t, int64(450000), alert.Usage)
	assert.Equal(t, int64(500000), alert.Limit)
}

func TestBudgetStore_CheckProjectThreshold_ZeroLimit(t *testing.T) {
	s := NewBudgetStore()
	var called bool
	s.CheckProjectThreshold("engineering", "backend", 0, 80.0, func(a BudgetAlert) {
		called = true
	}, slog.Default())
	assert.False(t, called)
}
