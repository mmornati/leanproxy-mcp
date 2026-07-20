package budget

type ProjectBudget struct {
	Daily   int64 `yaml:"daily"`
	Monthly int64 `yaml:"monthly"`
}

type TeamBudget struct {
	Daily      int64                    `yaml:"daily"`
	Monthly    int64                    `yaml:"monthly"`
	WebhookURL string                   `yaml:"webhook_url"`
	HardCap    bool                     `yaml:"hard_cap"`
	SoftCapPct *float64                 `yaml:"soft_cap_pct"`
	Projects   map[string]ProjectBudget `yaml:"projects"`
}

func (t *TeamBudget) SoftCapPercentage() float64 {
	if t.SoftCapPct != nil {
		return *t.SoftCapPct
	}
	return 90.0
}

type BudgetConfig struct {
	WebhookURL string                `yaml:"webhook_url"`
	Teams      map[string]TeamBudget `yaml:"teams"`
}

func (c *BudgetConfig) Enabled() bool {
	return c != nil && len(c.Teams) > 0
}

func (c *BudgetConfig) Team(name string) *TeamBudget {
	if c == nil || c.Teams == nil {
		return nil
	}
	t, ok := c.Teams[name]
	if !ok {
		return nil
	}
	return &t
}
