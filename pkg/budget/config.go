package budget

type ProjectBudget struct {
	Daily   int64 `yaml:"daily"`
	Monthly int64 `yaml:"monthly"`
}

type TeamBudget struct {
	Daily     int64                    `yaml:"daily"`
	Monthly   int64                    `yaml:"monthly"`
	WebhookURL string                  `yaml:"webhook_url"`
	Projects  map[string]ProjectBudget `yaml:"projects"`
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
