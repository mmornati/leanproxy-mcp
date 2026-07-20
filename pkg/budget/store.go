package budget

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/ratelimit"
)

type TeamUsage struct {
	mu          sync.Mutex
	dailyBucket *ratelimit.TokenBucket
	monthlyUsed int64
	resetDay    time.Time
	resetMonth  time.Time
}

type ProjectUsage struct {
	mu          sync.Mutex
	monthlyUsed int64
	resetMonth  time.Time
}

type BudgetStore struct {
	mu       sync.RWMutex
	teams    map[string]*TeamUsage
	projects map[string]map[string]*ProjectUsage
}

func NewBudgetStore() *BudgetStore {
	return &BudgetStore{
		teams:    make(map[string]*TeamUsage),
		projects: make(map[string]map[string]*ProjectUsage),
	}
}

func (s *BudgetStore) EnsureTeam(name string, dailyLimit int64) *TeamUsage {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t, ok := s.teams[name]; ok {
		return t
	}

	now := time.Now()
	dayStart := now.Truncate(24 * time.Hour)
	refillInterval := 24 * time.Hour
	if dailyLimit <= 0 {
		slog.Warn("team %q daily limit set to %d, treating as unlimited", name, dailyLimit)
		dailyLimit = math.MaxInt32
	}
	t := &TeamUsage{
		dailyBucket: ratelimit.NewTokenBucket(int(dailyLimit), refillInterval),
		resetDay:    dayStart,
		resetMonth:  time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()),
	}
	s.teams[name] = t
	return t
}

func (s *BudgetStore) EnsureProject(team, project string) *ProjectUsage {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.projects[team] == nil {
		s.projects[team] = make(map[string]*ProjectUsage)
	}
	if p, ok := s.projects[team][project]; ok {
		return p
	}

	now := time.Now()
	p := &ProjectUsage{
		resetMonth: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()),
	}
	s.projects[team][project] = p
	return p
}

func (s *BudgetStore) DeductTeam(team string, tokens int64, dailyLimit int64) (remaining int, err error) {
	t := s.EnsureTeam(team, dailyLimit)
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	dayStart := now.Truncate(24 * time.Hour)
	if dayStart.After(t.resetDay) {
		t.dailyBucket = ratelimit.NewTokenBucket(int(dailyLimit), 24*time.Hour)
		t.resetDay = dayStart
	}
	if now.Year() != t.resetMonth.Year() || now.Month() != t.resetMonth.Month() {
		t.monthlyUsed = 0
		t.resetMonth = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	}

	if !t.dailyBucket.AllowN(int(tokens)) {
		return 0, fmt.Errorf("team %q daily budget exceeded", team)
	}

	t.monthlyUsed += tokens
	return t.dailyBucket.Remaining(), nil
}

func (s *BudgetStore) RefundTeam(team string, tokens int64) {
	s.mu.RLock()
	t, ok := s.teams[team]
	s.mu.RUnlock()
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.monthlyUsed -= tokens
	if t.monthlyUsed < 0 {
		t.monthlyUsed = 0
	}
	t.dailyBucket.AddN(int(tokens))
}

func (s *BudgetStore) DeductProject(team, project string, tokens int64, monthlyLimit int64) (int64, error) {
	p := s.EnsureProject(team, project)
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if now.Year() != p.resetMonth.Year() || now.Month() != p.resetMonth.Month() {
		p.monthlyUsed = 0
		p.resetMonth = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	}

	if monthlyLimit > 0 && p.monthlyUsed+tokens > monthlyLimit {
		return p.monthlyUsed, fmt.Errorf("project %q/%q monthly budget exceeded", team, project)
	}

	p.monthlyUsed += tokens
	return p.monthlyUsed, nil
}

func (s *BudgetStore) TeamDailyRemaining(team string) int {
	s.mu.RLock()
	t, ok := s.teams[team]
	s.mu.RUnlock()
	if !ok {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dailyBucket.Remaining()
}

func (s *BudgetStore) TeamMonthlyUsed(team string) int64 {
	s.mu.RLock()
	t, ok := s.teams[team]
	s.mu.RUnlock()
	if !ok {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.monthlyUsed
}

func (s *BudgetStore) ProjectMonthlyUsed(team, project string) int64 {
	s.mu.RLock()
	pm, ok := s.projects[team]
	if !ok {
		s.mu.RUnlock()
		return 0
	}
	p, ok := pm[project]
	s.mu.RUnlock()
	if !ok {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.monthlyUsed
}

func (c *TeamUsage) MonthlyUsed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.monthlyUsed
}

func (c *ProjectUsage) MonthlyUsed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.monthlyUsed
}

type BudgetAlert struct {
	Team       string
	Project    string
	Metric     string
	Usage      int64
	Limit      int64
	Percentage float64
}

func (a BudgetAlert) TeamName() string         { return a.Team }
func (a BudgetAlert) ProjectName() string      { return a.Project }
func (a BudgetAlert) MetricName() string       { return a.Metric }
func (a BudgetAlert) UsageAmount() int64       { return a.Usage }
func (a BudgetAlert) LimitAmount() int64       { return a.Limit }
func (a BudgetAlert) PercentageValue() float64 { return a.Percentage }

type AlertCallback func(BudgetAlert)

func (s *BudgetStore) CheckProjectThreshold(team, project string, monthlyLimit int64, thresholdPct float64, callback AlertCallback, logger *slog.Logger) {
	p := s.EnsureProject(team, project)
	p.mu.Lock()
	used := p.monthlyUsed
	if monthlyLimit <= 0 {
		p.mu.Unlock()
		return
	}
	pct := float64(used) / float64(monthlyLimit) * 100
	p.mu.Unlock()

	if pct >= thresholdPct {
		alert := BudgetAlert{
			Team:       team,
			Project:    project,
			Metric:     "monthly",
			Usage:      used,
			Limit:      monthlyLimit,
			Percentage: pct,
		}

		logger.Warn("budget threshold exceeded",
			"team", team,
			"project", project,
			"usage", used,
			"limit", monthlyLimit,
			"percentage", fmt.Sprintf("%.1f%%", pct),
		)

		if callback != nil {
			callback(alert)
		}
	}
}
