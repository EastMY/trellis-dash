package codexusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type modelPrice struct {
	Input       float64
	CachedInput float64
	Output      float64
}

var builtinPrices = map[string]modelPrice{
	"gpt-5.6-sol":             {Input: 5, CachedInput: 0.5, Output: 30},
	"gpt-5.6-terra":           {Input: 2.5, CachedInput: 0.25, Output: 15},
	"gpt-5.6-luna":            {Input: 1, CachedInput: 0.1, Output: 6},
	"gpt-5.5":                 {Input: 5, CachedInput: 0.5, Output: 30},
	"aimami_relay_e921877eed": {Input: 5, CachedInput: 0.5, Output: 30},
	"gpt-5.4":                 {Input: 2.5, CachedInput: 0.25, Output: 15},
	"gpt-5.4-mini":            {Input: 0.75, CachedInput: 0.075, Output: 4.5},
	"claude-opus-4-7":         {Input: 5, CachedInput: 0.5, Output: 25},
	"claude-4.7":              {Input: 5, CachedInput: 0.5, Output: 25},
}

type relayState struct {
	Providers []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"providers"`
}

func loadRelayPriceModels(codexHome string) map[string]string {
	data, err := os.ReadFile(filepath.Join(codexHome, "codexmate", "relay", "state.json"))
	if err != nil {
		return nil
	}
	var state relayState
	if json.Unmarshal(data, &state) != nil {
		return nil
	}
	models := make(map[string]string, len(state.Providers))
	for _, provider := range state.Providers {
		if provider.ID == "" {
			continue
		}
		if strings.HasPrefix(provider.ID, "aimami_relay_") {
			models[provider.ID] = inferRelayModel(provider.Name)
		} else if provider.Model != "" {
			models[provider.ID] = provider.Model
		}
	}
	return models
}

func inferRelayModel(name string) string {
	lower := strings.ToLower(name)
	for _, rule := range []struct{ contains, model string }{
		{"terra", "gpt-5.6-terra"},
		{"luna", "gpt-5.6-luna"},
		{"sol", "gpt-5.6-sol"},
		{"5.5", "gpt-5.5"},
		{"5.4", "gpt-5.4"},
		{"claude", "claude-opus-4-7"},
	} {
		if strings.Contains(lower, rule.contains) {
			return rule.model
		}
	}
	return "gpt-5.4"
}

func priceForModel(model string, relayModels map[string]string) (modelPrice, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if mapped := relayModels[model]; mapped != "" {
		normalized = strings.ToLower(mapped)
	} else if strings.HasPrefix(normalized, "aimami_relay_") {
		normalized = "gpt-5.4"
	}
	price, ok := builtinPrices[normalized]
	return price, ok
}

func usageCost(usage tokenUsage, price modelPrice) float64 {
	uncached := usage.InputTokens - usage.CachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	return (float64(uncached)*price.Input +
		float64(usage.CachedInputTokens)*price.CachedInput +
		float64(usage.OutputTokens)*price.Output) / 1_000_000
}
