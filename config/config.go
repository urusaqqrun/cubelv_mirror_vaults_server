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

	// CLI 資源控制
	CLIIdleTTLSec    int // main CLI idle timeout（秒），預設 300（5 分鐘）
	MaxWarmPool      int // 全域 warmup pool 上限，預設 5
	MaxGlobalCLI     int // 全域活躍 main CLI 上限（跨 user），預設 10
	MaxCLIPerUser    int // 單一 user 活躍 CLI 上限，預設 3
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
		CLIIdleTTLSec:       getEnvInt("CLI_IDLE_TTL_SEC", 300),
		MaxWarmPool:          getEnvInt("MAX_WARM_POOL", 5),
		MaxGlobalCLI:         getEnvInt("MAX_GLOBAL_CLI", 10),
		MaxCLIPerUser:        getEnvInt("MAX_CLI_PER_USER", 3),
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
