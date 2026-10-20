package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Price is one model's API price in USD per million tokens (waits-events-cost-cli D5). Figures computed from
// it are API-equivalent, for comparison and waste detection, not billing.
type Price struct {
	Input      float64 `yaml:"input" json:"input"`
	Output     float64 `yaml:"output" json:"output"`
	CacheRead  float64 `yaml:"cache_read" json:"cache_read"`
	CacheWrite float64 `yaml:"cache_write" json:"cache_write"`
}

// Prices maps a model name to its price.
type Prices map[string]Price

// LoadPrices reads configs/prices.yaml under root; a missing file is an empty table.
func LoadPrices(root string) (Prices, error) {
	b, err := os.ReadFile(filepath.Join(root, "configs", "prices.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return Prices{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Models Prices `yaml:"models"`
	}
	if err = yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("configs/prices.yaml: %w", err)
	}
	for m, p := range doc.Models {
		if p.Input < 0 || p.Output < 0 || p.CacheRead < 0 || p.CacheWrite < 0 {
			return nil, fmt.Errorf("configs/prices.yaml: model %s: prices must not be negative", m)
		}
	}
	if doc.Models == nil {
		doc.Models = Prices{}
	}
	return doc.Models, nil
}

// Cost prices token counts of the model; false when the table has no price for it.
func (p Prices) Cost(model string, input, output, cacheRead, cacheWrite int64) (float64, bool) {
	price, ok := p[model]
	if !ok {
		return 0, false
	}
	return (float64(input)*price.Input + float64(output)*price.Output + float64(cacheRead)*price.CacheRead + float64(cacheWrite)*price.CacheWrite) / 1e6, true
}
