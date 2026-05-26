package dataloader

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadBudgetConfig parses tools/graphql-budgets.yml from path. Returns
// a BudgetConfig populated with defaults applied — callers can pass
// the returned config straight to CheckSnapshot.
//
// Path is resolved verbatim; callers running from the repo root
// typically pass "tools/graphql-budgets.yml".
func LoadBudgetConfig(path string) (BudgetConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BudgetConfig{}, fmt.Errorf("graphql-budgets: read %s: %w", path, err)
	}
	var cfg BudgetConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return BudgetConfig{}, fmt.Errorf("graphql-budgets: parse %s: %w", path, err)
	}
	if cfg.DefaultMaxBatchRoundTrips <= 0 {
		cfg.DefaultMaxBatchRoundTrips = 4
	}
	return cfg, nil
}
