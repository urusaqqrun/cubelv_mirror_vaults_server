package executor

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestVaultLock_LockUnlock(t *testing.T) {
	l := NewVaultLock()

	if !l.Lock("user1", "task-1") {
		t.Error("first lock should succeed")
	}
	if l.Lock("user1", "task-2") {
		t.Error("second lock by different task should fail")
	}
	if !l.IsLocked("user1") {
		t.Error("user1 should be locked")
	}
	if l.GetLockingTask("user1") != "task-1" {
		t.Errorf("locking task: got %q, want %q", l.GetLockingTask("user1"), "task-1")
	}

	l.Unlock("user1", "task-1")
	if l.IsLocked("user1") {
		t.Error("user1 should be unlocked after unlock")
	}
}

func TestVaultLock_SameTaskRelock(t *testing.T) {
	l := NewVaultLock()
	l.Lock("user1", "task-1")
	if !l.Lock("user1", "task-1") {
		t.Error("same task should be able to relock")
	}
}

func TestVaultLock_UnlockWrongTask(t *testing.T) {
	l := NewVaultLock()
	l.Lock("user1", "task-1")
	l.Unlock("user1", "task-wrong")

	if !l.IsLocked("user1") {
		t.Error("wrong task unlock should not release the lock")
	}
}

func TestVaultLock_MultiUser(t *testing.T) {
	l := NewVaultLock()
	l.Lock("user1", "task-1")
	l.Lock("user2", "task-2")

	if !l.IsLocked("user1") || !l.IsLocked("user2") {
		t.Error("both users should be locked")
	}
	if l.IsLocked("user3") {
		t.Error("user3 should not be locked")
	}
}

func TestRedisVaultLock_LockUnlock(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	l := NewRedisVaultLock(rdb, 5*time.Minute)

	if !l.Lock("user1", "task-1") {
		t.Fatal("first lock should succeed")
	}
	if l.Lock("user1", "task-2") {
		t.Fatal("second lock by different task should fail")
	}
	if !l.IsLocked("user1") {
		t.Fatal("user1 should be locked")
	}
	if got := l.GetLockingTask("user1"); got != "task-1" {
		t.Fatalf("locking task: got %q, want %q", got, "task-1")
	}

	l.Unlock("user1", "task-wrong")
	if !l.IsLocked("user1") {
		t.Fatal("wrong task unlock should not release lock")
	}

	l.Unlock("user1", "task-1")
	if l.IsLocked("user1") {
		t.Fatal("user1 should be unlocked after correct unlock")
	}
}

func TestRedisVaultLock_SameTaskRelockRefreshTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	l := NewRedisVaultLock(rdb, 10*time.Second)
	if !l.Lock("user1", "task-1") {
		t.Fatal("first lock should succeed")
	}
	ttl1 := mr.TTL("vault:lock:user1")
	mr.FastForward(6 * time.Second)

	if !l.Lock("user1", "task-1") {
		t.Fatal("same task relock should succeed")
	}
	ttl2 := mr.TTL("vault:lock:user1")
	if ttl2 <= ttl1-6*time.Second {
		t.Fatalf("expected ttl refresh, before=%v after=%v", ttl1, ttl2)
	}
}
