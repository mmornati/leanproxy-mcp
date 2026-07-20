package budget

import (
	"fmt"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/webhook"
)

const defaultThresholdPct = 80.0

type Governor struct {
	store        *BudgetStore
	config       *BudgetConfig
	logger       *slog.Logger
	thresholdPct float64
}

func NewGovernor(store *BudgetStore, config *BudgetConfig, logger *slog.Logger) *Governor {
	if logger == nil {
		logger = slog.Default()
	}

	return &Governor{
		store:        store,
		config:       config,
		logger:       logger,
		thresholdPct: defaultThresholdPct,
	}
}

func (g *Governor) Enabled() bool {
	return g.config != nil && g.config.Enabled()
}

func (g *Governor) Deduct(team, project string, tokens int64) error {
	if !g.Enabled() {
		return nil
	}

	teamCfg := g.config.Team(team)
	if teamCfg == nil {
		return nil
	}

	if project != "" {
		projCfg, ok := teamCfg.Projects[project]
		if ok && projCfg.Monthly > 0 {
			used := g.store.ProjectMonthlyUsed(team, project)
			if used+tokens > projCfg.Monthly {
				return fmt.Errorf("project %q/%q monthly budget exceeded", team, project)
			}
		}
	}

	if teamCfg.Daily > 0 {
		_, err := g.store.DeductTeam(team, tokens, teamCfg.Daily)
		if err != nil {
			return err
		}
	}

	if project != "" {
		projCfg, ok := teamCfg.Projects[project]
		if ok && projCfg.Monthly > 0 {
			_, err := g.store.DeductProject(team, project, tokens, projCfg.Monthly)
			if err != nil {
				g.store.RefundTeam(team, tokens)
				return err
			}

			whURL := teamCfg.WebhookURL
			if whURL == "" {
				whURL = g.config.WebhookURL
			}

			alertFn := g.buildAlertCallback(whURL)
			g.store.CheckProjectThreshold(team, project, projCfg.Monthly, g.thresholdPct, alertFn, g.logger)
		}
	}

	return nil
}

func (g *Governor) buildAlertCallback(webhookURL string) AlertCallback {
	if webhookURL == "" {
		return nil
	}

	localDispatcher := webhook.NewDispatcher(webhookURL, g.logger)
	return func(a BudgetAlert) {
		if err := localDispatcher.SendAlert(a); err != nil {
			g.logger.Error("webhook alert failed",
				"team", a.Team,
				"project", a.Project,
				"error", err,
			)
		}
	}
}
