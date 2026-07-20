package budget

import (
	"fmt"
	"log/slog"
)

type BudgetAction int

const (
	BudgetActionAllow    BudgetAction = 0
	BudgetActionDowngrade BudgetAction = 1
	BudgetActionReject   BudgetAction = 2
)

func (a BudgetAction) String() string {
	switch a {
	case BudgetActionAllow:
		return "allow"
	case BudgetActionDowngrade:
		return "downgrade"
	case BudgetActionReject:
		return "reject"
	default:
		return "unknown"
	}
}

type BudgetDecision struct {
	Action      BudgetAction
	Message     string
	Team        string
	Project     string
	DailyPct    float64
	MonthlyPct  float64
	Err         error
}

type BudgetExceededError struct {
	Team    string
	Project string
	Daily   bool
	Monthly bool
	HardCap bool
}

func (e *BudgetExceededError) Error() string {
	if e.HardCap {
		return fmt.Sprintf("budget exceeded (hard cap): team %q reached daily limit", e.Team)
	}
	if e.Daily && e.Monthly {
		return fmt.Sprintf("budget exceeded: team %q daily and monthly budget consumed", e.Team)
	}
	if e.Daily {
		return fmt.Sprintf("budget exceeded: team %q daily budget consumed", e.Team)
	}
	return fmt.Sprintf("budget exceeded: team %q monthly budget consumed", e.Team)
}

func EvaluateBudget(team, project string, store *BudgetStore, config *BudgetConfig, ignoreBudget bool, logger *slog.Logger) BudgetDecision {
	if !config.Enabled() {
		return BudgetDecision{Action: BudgetActionAllow}
	}

	teamCfg := config.Team(team)
	if teamCfg == nil {
		return BudgetDecision{Action: BudgetActionAllow}
	}

	if ignoreBudget {
		logger.Debug("budget: ignore-budget flag set, allowing request",
			"team", team,
			"project", project,
		)
		return BudgetDecision{
			Action: BudgetActionAllow,
			Team:   team,
			Project: project,
		}
	}

	dailyLimit := teamCfg.Daily
	monthlyLimit := teamCfg.Monthly

	// Ensure the team entry exists in the store before reading usage.
	store.EnsureTeam(team, dailyLimit)

	remaining := store.TeamDailyRemaining(team)
	monthlyUsed := store.TeamMonthlyUsed(team)

	var dailyPct, monthlyPct float64
	if dailyLimit > 0 {
		dailyPct = 100.0 - (float64(remaining)/float64(dailyLimit))*100.0
	} else {
		dailyPct = 0
	}
	if monthlyLimit > 0 {
		monthlyPct = float64(monthlyUsed) / float64(monthlyLimit) * 100.0
	} else {
		monthlyPct = 0
	}

	dailyExceeded := dailyLimit > 0 && remaining <= 0
	monthlyExceeded := monthlyLimit > 0 && monthlyUsed >= monthlyLimit

	softCapPct := teamCfg.SoftCapPercentage()

	if dailyExceeded {
		exceededErr := &BudgetExceededError{
			Team:    team,
			Project: project,
			Daily:   true,
			HardCap: teamCfg.HardCap,
		}
		return BudgetDecision{
			Action:    BudgetActionReject,
			Message:   exceededErr.Error(),
			Err:       exceededErr,
			Team:      team,
			Project:   project,
			DailyPct:  100.0,
			MonthlyPct: monthlyPct,
		}
	}

	if monthlyExceeded {
		exceededErr := &BudgetExceededError{
			Team:    team,
			Project: project,
			Monthly: true,
		}
		return BudgetDecision{
			Action:    BudgetActionReject,
			Message:   exceededErr.Error(),
			Err:       exceededErr,
			Team:      team,
			Project:   project,
			DailyPct:  dailyPct,
			MonthlyPct: 100.0,
		}
	}

	// Check hard cap independently of daily/monthly exhaustion.
	// If hard cap is enabled and the soft cap threshold is met, reject.
	if teamCfg.HardCap && (dailyPct >= softCapPct || monthlyPct >= softCapPct) {
		exceededErr := &BudgetExceededError{
			Team:    team,
			Project: project,
			HardCap: true,
		}
		return BudgetDecision{
			Action:    BudgetActionReject,
			Message:   exceededErr.Error(),
			Err:       exceededErr,
			Team:      team,
			Project:   project,
			DailyPct:  dailyPct,
			MonthlyPct: monthlyPct,
		}
	}

	if dailyPct >= softCapPct || monthlyPct >= softCapPct {
		return BudgetDecision{
			Action:    BudgetActionDowngrade,
			Message:   fmt.Sprintf("budget threshold reached for team %q (daily: %.1f%%, monthly: %.1f%%)", team, dailyPct, monthlyPct),
			Team:      team,
			Project:   project,
			DailyPct:  dailyPct,
			MonthlyPct: monthlyPct,
		}
	}

	return BudgetDecision{
		Action:    BudgetActionAllow,
		Team:      team,
		Project:   project,
		DailyPct:  dailyPct,
		MonthlyPct: monthlyPct,
	}
}
