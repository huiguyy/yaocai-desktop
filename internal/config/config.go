package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// SpiderConfig holds the runtime configuration
type SpiderConfig struct {
	PriceAnomalyThreshold float64 `json:"price_anomaly_threshold"`
	Port                  int     `json:"port"`
	Debug                 bool    `json:"debug"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *SpiderConfig {
	return &SpiderConfig{
		PriceAnomalyThreshold: 9999,
		Port:                  14391,
		Debug:                 false,
	}
}

var (
	cfg     *SpiderConfig
	cfgOnce sync.Once
	cfgMu   sync.RWMutex
)

// GetConfig returns the current config (thread-safe)
func GetConfig() *SpiderConfig {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if cfg == nil {
		return DefaultConfig()
	}
	// Return a copy
	c := *cfg
	return &c
}

// SetConfig updates the config (thread-safe) and persists to file
func SetConfig(newCfg *SpiderConfig) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = newCfg
	return saveConfig(newCfg)
}

// UpdateConfig merges partial config updates
func UpdateConfig(partial map[string]interface{}) (*SpiderConfig, error) {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	if cfg == nil {
		cfg = DefaultConfig()
	}

	if v, ok := partial["price_anomaly_threshold"]; ok {
		if f, ok := v.(float64); ok {
			cfg.PriceAnomalyThreshold = f
		}
	}
	if v, ok := partial["port"]; ok {
		if f, ok := v.(float64); ok {
			cfg.Port = int(f)
		}
	}
	if v, ok := partial["debug"]; ok {
		if b, ok := v.(bool); ok {
			cfg.Debug = b
		}
	}

	result := *cfg
	if err := saveConfig(cfg); err != nil {
		return nil, err
	}
	return &result, nil
}

// LoadConfig loads config from file or creates default
func LoadConfig(path string) (*SpiderConfig, error) {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = DefaultConfig()
			return cfg, saveConfig(cfg)
		}
		return nil, err
	}

	cfg = DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}
	return cfg, nil
}

func saveConfig(c *SpiderConfig) error {
	configPath := "spider_config.json"
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}
