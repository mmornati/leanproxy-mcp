package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type BudgetAlertPayload struct {
	Team       string  `json:"team"`
	Project    string  `json:"project,omitempty"`
	Metric     string  `json:"metric"`
	Usage      int64   `json:"usage"`
	Limit      int64   `json:"limit"`
	Percentage float64 `json:"percentage"`
	Timestamp  string  `json:"timestamp"`
}

type Dispatcher struct {
	webhookURL string
	client     *http.Client
	logger     *slog.Logger
}

func NewDispatcher(webhookURL string, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}

	return &Dispatcher{
		webhookURL: webhookURL,
		logger:     logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type BudgetAlert interface {
	TeamName() string
	ProjectName() string
	MetricName() string
	UsageAmount() int64
	LimitAmount() int64
	PercentageValue() float64
}

func (d *Dispatcher) SendAlert(alert interface{}) error {
	if d.webhookURL == "" {
		return nil
	}

	var payload BudgetAlertPayload

	switch a := alert.(type) {
	case BudgetAlert:
		payload = BudgetAlertPayload{
			Team:       a.TeamName(),
			Project:    a.ProjectName(),
			Metric:     a.MetricName(),
			Usage:      a.UsageAmount(),
			Limit:      a.LimitAmount(),
			Percentage: a.PercentageValue(),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}
	default:
		data, err := json.Marshal(alert)
		if err != nil {
			return fmt.Errorf("webhook: marshal alert: %w", err)
		}
		var static struct {
			Team       string  `json:"team"`
			Project    string  `json:"project"`
			Metric     string  `json:"metric"`
			Usage      int64   `json:"usage"`
			Limit      int64   `json:"limit"`
			Percentage float64 `json:"percentage"`
		}
		if err := json.Unmarshal(data, &static); err == nil {
			payload = BudgetAlertPayload{
				Team:       static.Team,
				Project:    static.Project,
				Metric:     static.Metric,
				Usage:      static.Usage,
				Limit:      static.Limit,
				Percentage: static.Percentage,
				Timestamp:  time.Now().UTC().Format(time.RFC3339),
			}
		} else {
			payload = BudgetAlertPayload{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}

	resp, err := d.client.Post(d.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: post request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: unexpected status %d", resp.StatusCode)
	}

	d.logger.Debug("webhook alert sent",
		"url", d.webhookURL,
		"team", payload.Team,
		"project", payload.Project,
		"percentage", payload.Percentage,
	)

	return nil
}
