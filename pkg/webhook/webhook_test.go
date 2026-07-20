package webhook

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockAlert struct {
	team    string
	project string
	metric  string
	usage   int64
	limit   int64
	pct     float64
}

func (m mockAlert) TeamName() string         { return m.team }
func (m mockAlert) ProjectName() string      { return m.project }
func (m mockAlert) MetricName() string       { return m.metric }
func (m mockAlert) UsageAmount() int64       { return m.usage }
func (m mockAlert) LimitAmount() int64       { return m.limit }
func (m mockAlert) PercentageValue() float64 { return m.pct }

func TestNewDispatcher(t *testing.T) {
	d := NewDispatcher("https://hooks.example.com", slog.Default())
	assert.NotNil(t, d)
	assert.Equal(t, "https://hooks.example.com", d.webhookURL)
}

func TestNewDispatcher_NilLogger(t *testing.T) {
	d := NewDispatcher("https://hooks.example.com", nil)
	assert.NotNil(t, d)
}

func TestDispatcher_EmptyURL(t *testing.T) {
	d := NewDispatcher("", slog.Default())
	assert.NotNil(t, d)

	err := d.SendAlert(mockAlert{team: "eng"})
	assert.NoError(t, err)
}

func TestDispatcher_SendAlert_BudgetAlertInterface(t *testing.T) {
	d := NewDispatcher("", slog.Default())

	alert := mockAlert{
		team:    "engineering",
		project: "backend",
		metric:  "monthly",
		usage:   400000,
		limit:   500000,
		pct:     80.0,
	}

	err := d.SendAlert(alert)
	assert.NoError(t, err)
}

func TestDispatcher_SendAlert_InvalidURL(t *testing.T) {
	d := NewDispatcher("http://127.0.0.1:1", slog.Default())

	err := d.SendAlert(mockAlert{team: "eng", usage: 100, limit: 200, pct: 50.0})
	assert.Error(t, err)
}

func TestBudgetAlertPayload_Marshal(t *testing.T) {
	alert := mockAlert{
		team:    "eng",
		project: "web",
		metric:  "daily",
		usage:   80000,
		limit:   100000,
		pct:     80.0,
	}
	d := NewDispatcher("", slog.Default())
	err := d.SendAlert(alert)
	assert.NoError(t, err)
}
