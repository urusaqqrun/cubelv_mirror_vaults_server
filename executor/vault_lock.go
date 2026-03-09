package executor

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// VaultLocker 抽象 Vault 鎖，支援本機與分散式實作。
type VaultLocker interface {
	Lock(userId, taskID string) bool
	Unlock(userId, taskID string)
	IsLocked(userId string) bool
	GetLockingTask(userId string) string
}

// VaultLock 控制 AI 任務期間暫停該用戶的 Vault 同步
type VaultLock struct {
	mu    sync.RWMutex
	locks map[string]string // userId → taskID
}

func NewVaultLock() *VaultLock {
	return &VaultLock{locks: make(map[string]string)}
}

// Lock 鎖定用戶的 Vault（回傳 false 表示已被其他任務鎖定）
func (l *VaultLock) Lock(userId, taskID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.locks[userId]; ok && existing != taskID {
		return false
	}
	l.locks[userId] = taskID
	return true
}

// Unlock 解鎖用戶的 Vault
func (l *VaultLock) Unlock(userId, taskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.locks[userId] == taskID {
		delete(l.locks, userId)
	}
}

// IsLocked 檢查用戶的 Vault 是否被鎖定
func (l *VaultLock) IsLocked(userId string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.locks[userId]
	return ok
}

// GetLockingTask 取得鎖定的 taskID（空字串表示未鎖定）
func (l *VaultLock) GetLockingTask(userId string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.locks[userId]
}

// RedisVaultLock 使用 Redis 實作跨實例分散式鎖。
type RedisVaultLock struct {
	rdb    *redis.Client
	ttl    time.Duration
	prefix string
}

func NewRedisVaultLock(rdb *redis.Client, ttl time.Duration) *RedisVaultLock {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &RedisVaultLock{
		rdb:    rdb,
		ttl:    ttl,
		prefix: "vault:lock:",
	}
}

func (l *RedisVaultLock) key(userId string) string {
	return l.prefix + userId
}

// Lock 以 SET NX + value compare 方式獲取鎖；同 task 可重入並續期。
func (l *RedisVaultLock) Lock(userId, taskID string) bool {
	if l.rdb == nil || userId == "" || taskID == "" {
		return false
	}
	ctx := context.Background()
	key := l.key(userId)

	// 先嘗試直接搶鎖（分散式互斥）
	ok, err := l.rdb.SetNX(ctx, key, taskID, l.ttl).Result()
	if err != nil {
		return false
	}
	if ok {
		return true
	}

	// 若同一 task 重入，允許續期
	current, err := l.rdb.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	if current != taskID {
		return false
	}
	return l.rdb.Expire(ctx, key, l.ttl).Err() == nil
}

// Unlock 僅在 value 符合 taskID 時刪除，避免誤解鎖。
func (l *RedisVaultLock) Unlock(userId, taskID string) {
	if l.rdb == nil || userId == "" || taskID == "" {
		return
	}
	ctx := context.Background()
	key := l.key(userId)
	const unlockLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`
	_ = l.rdb.Eval(ctx, unlockLua, []string{key}, taskID).Err()
}

func (l *RedisVaultLock) IsLocked(userId string) bool {
	if l.rdb == nil || userId == "" {
		return false
	}
	ctx := context.Background()
	n, err := l.rdb.Exists(ctx, l.key(userId)).Result()
	return err == nil && n > 0
}

func (l *RedisVaultLock) GetLockingTask(userId string) string {
	if l.rdb == nil || userId == "" {
		return ""
	}
	ctx := context.Background()
	v, err := l.rdb.Get(ctx, l.key(userId)).Result()
	if err != nil {
		return ""
	}
	return v
}
