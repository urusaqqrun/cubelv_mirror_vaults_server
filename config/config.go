package config

import "os"

type Config struct {
	PostgresURI  string
	RedisURI     string
	VaultRoot    string
	AnthropicKey string
	Port         string

	// 同步 worker
	SyncLoopIntervalSec int
	SyncCursorLeaseSec  int
	SyncOwnerScanLimit  int
	SyncChangeBatchSize int
}

func Load() *Config {
	return &Config{
		PostgresURI:         getEnv("POSTGRES_URI", ""),
		RedisURI:            getEnv("REDIS_URI", "localhost:6379"),
		VaultRoot:           getEnv("VAULT_ROOT", "/vaults"),
		AnthropicKey:        getEnv("ANTHROPIC_API_KEY", ""),
		Port:                getEnv("PORT", "8080"),
		SyncLoopIntervalSec: getEnvInt("SYNC_LOOP_INTERVAL_SEC", 30),
		SyncCursorLeaseSec:  getEnvInt("SYNC_CURSOR_LEASE_SEC", 120),
		SyncOwnerScanLimit:  getEnvInt("SYNC_OWNER_SCAN_LIMIT", 128),
		SyncChangeBatchSize: getEnvInt("SYNC_CHANGE_BATCH_SIZE", 100),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	for _, c := range v {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}
