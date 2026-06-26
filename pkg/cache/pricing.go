package cache

import "strings"

type ModelPricing struct {
	ModelName              string
	InputCostPerMTok       float64
	CachedInputCostPerMTok float64
	OutputCostPerMTok      float64
}

var pricingTable = []ModelPricing{
	{
		ModelName:              "claude-sonnet-4-20250514",
		InputCostPerMTok:       3.0,
		CachedInputCostPerMTok: 0.30,
		OutputCostPerMTok:      15.0,
	},
	{
		ModelName:              "claude-3-5-sonnet-20241022",
		InputCostPerMTok:       3.0,
		CachedInputCostPerMTok: 0.30,
		OutputCostPerMTok:      15.0,
	},
	{
		ModelName:              "claude-3-5-haiku-20241022",
		InputCostPerMTok:       0.80,
		CachedInputCostPerMTok: 0.08,
		OutputCostPerMTok:      4.0,
	},
	{
		ModelName:              "claude-3-opus-20240229",
		InputCostPerMTok:       15.0,
		CachedInputCostPerMTok: 1.50,
		OutputCostPerMTok:      75.0,
	},
	{
		ModelName:              "claude-3-haiku-20240307",
		InputCostPerMTok:       0.25,
		CachedInputCostPerMTok: 0.03,
		OutputCostPerMTok:      1.25,
	},
}

var defaultModel = "claude-sonnet-4-20250514"

func SupportedModelList() string {
	names := make([]string, 0, len(pricingTable))
	for _, p := range pricingTable {
		names = append(names, p.ModelName)
	}
	return strings.Join(names, ", ")
}

func ModelCost(model string) (ModelPricing, bool) {
	if model == "" {
		model = defaultModel
	}
	for _, p := range pricingTable {
		if p.ModelName == model {
			return p, true
		}
	}
	return ModelPricing{}, false
}

func CalculateTokenSavingsCost(model string, tokens int64) float64 {
	if tokens <= 0 {
		return 0
	}
	price, ok := ModelCost(model)
	if !ok {
		return 0
	}
	savingsPerMTok := price.InputCostPerMTok - price.CachedInputCostPerMTok
	return float64(tokens) / 1_000_000.0 * savingsPerMTok
}
