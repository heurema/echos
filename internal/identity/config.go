package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const defaultRelayURL = "http://localhost:8080"

type config struct {
	RelayURL string `json:"relay_url"`
}

// RelayURL resolves the relay base URL: $ECHOS_RELAY, else
// ~/.config/echos/config.json's relay_url, else the localhost default.
func RelayURL(dir string) string {
	if v := os.Getenv("ECHOS_RELAY"); v != "" {
		return v
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		var cfg config
		if json.Unmarshal(raw, &cfg) == nil && cfg.RelayURL != "" {
			return cfg.RelayURL
		}
	}
	return defaultRelayURL
}
