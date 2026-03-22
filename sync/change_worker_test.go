package sync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockChangeStore struct {
	mu           sync.Mutex
	owners       []string
	cursors      map[string]*CursorState
	changes      map[string][]ChangeRecord
	latestSeq    map[string]int
	backlogStats BacklogStats
}

func newMockChangeStore() *mockChangeStore {
	return &mockChangeStore{
		cursors:   make(map[string]*CursorState),
		changes:   make(map[string][]ChangeRecord),
		latestSeq: make(map[string]int),
	}
}

func (s *mockChangeStore) ListOwnersForSync(_ context.Context, _ int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.owners))
	copy(out, s.owners)
	return out, nil
}

func (s *mockChangeStore) AcquireCursorLease(_ context.Context, ownerUserID, leaseOwner string, leaseTTL time.Duration) (*CursorState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor, ok := s.cursors[ownerUserID]
	if !ok {
		cursor = &CursorState{OwnerUserID: ownerUserID}
		s.cursors[ownerUserID] = cursor
	}
	cursor.LeaseOwner = leaseOwner
	cursor.LeaseUntilMs = time.Now().Add(leaseTTL).UnixMilli()
	clone := *cursor
	return &clone, true, nil
}

func (s *mockChangeStore) ReleaseCursorLease(_ context.Context, ownerUserID, leaseOwner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor := s.cursors[ownerUserID]
	if cursor != nil && cursor.LeaseOwner == leaseOwner {
		cursor.LeaseOwner = ""
		cursor.LeaseUntilMs = 0
	}
	return nil
}

func (s *mockChangeStore) GetLatestSeq(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestSeq[userID], nil
}

func (s *mockChangeStore) GetChangesAfterSeq(_ context.Context, userID string, afterSeq, limit int) ([]ChangeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ChangeRecord
	for _, change := range s.changes[userID] {
		if change.Seq > afterSeq {
			out = append(out, change)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *mockChangeStore) MarkCursorInitialized(_ context.Context, ownerUserID, _ string, seq int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor := s.cursors[ownerUserID]
	cursor.Initialized = true
	cursor.LastSeq = seq
	cursor.LastError = ""
	return nil
}

func (s *mockChangeStore) AdvanceCursor(_ context.Context, ownerUserID, _ string, seq int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor := s.cursors[ownerUserID]
	cursor.LastSeq = seq
	cursor.LastError = ""
	return nil
}

func (s *mockChangeStore) RecordCursorError(_ context.Context, ownerUserID, _ string, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursor := s.cursors[ownerUserID]
	cursor.LastError = message
	return nil
}

func (s *mockChangeStore) GetBacklogStats(_ context.Context) (BacklogStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backlogStats, nil
}

type mockBootstrapper struct {
	mu    sync.Mutex
	users []string
}

func (b *mockBootstrapper) BootstrapOwner(_ context.Context, ownerUserID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.users = append(b.users, ownerUserID)
	return nil
}

type mockProcessor struct {
	mu        sync.Mutex
	processed []ChangeRecord
	failSeq   int
}

func (p *mockProcessor) ProcessChange(_ context.Context, change ChangeRecord) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failSeq > 0 && change.Seq == p.failSeq {
		return errors.New("mock processor failure")
	}
	p.processed = append(p.processed, change)
	return nil
}

type mockLocker struct {
	locked map[string]bool
}

func (l *mockLocker) IsLocked(userId string) bool {
	return l.locked[userId]
}

type mockHandler struct {
	events []SyncEvent
}

func (h *mockHandler) HandleEvent(_ context.Context, event SyncEvent) error {
	h.events = append(h.events, event)
	return nil
}

func TestChangeWorkerBootstrapOwner(t *testing.T) {
	store := newMockChangeStore()
	store.owners = []string{"u1"}
	store.cursors["u1"] = &CursorState{OwnerUserID: "u1"}
	store.latestSeq["u1"] = 42

	bootstrapper := &mockBootstrapper{}
	processor := &mockProcessor{}
	worker := NewChangeWorker(store, processor, bootstrapper, nil, time.Second, "worker-1")

	if err := worker.processOwner(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if !store.cursors["u1"].Initialized {
		t.Fatal("expected cursor to be initialized")
	}
	if store.cursors["u1"].LastSeq != 42 {
		t.Fatalf("expected last seq 42, got %d", store.cursors["u1"].LastSeq)
	}
	if len(bootstrapper.users) != 1 || bootstrapper.users[0] != "u1" {
		t.Fatalf("unexpected bootstrap calls: %#v", bootstrapper.users)
	}
}

func TestChangeWorkerProcessesAndAdvancesCursor(t *testing.T) {
	store := newMockChangeStore()
	store.owners = []string{"u1"}
	store.cursors["u1"] = &CursorState{OwnerUserID: "u1", LastSeq: 10, Initialized: true}
	store.changes["u1"] = []ChangeRecord{
		{Seq: 11, UserID: "u1", ItemID: "a", ChangeType: "updated"},
		{Seq: 12, UserID: "u1", ItemID: "b", ChangeType: "deleted"},
	}

	worker := NewChangeWorker(store, &mockProcessor{}, &mockBootstrapper{}, nil, time.Second, "worker-1")
	if err := worker.processOwner(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := store.cursors["u1"].LastSeq; got != 12 {
		t.Fatalf("expected last seq 12, got %d", got)
	}
	snapshot := worker.Snapshot()
	if snapshot.LastProcessedOwner != "u1" || snapshot.LastProcessedSeq != 12 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestChangeWorkerStopsAndRecordsError(t *testing.T) {
	store := newMockChangeStore()
	store.cursors["u1"] = &CursorState{OwnerUserID: "u1", LastSeq: 10, Initialized: true}
	store.changes["u1"] = []ChangeRecord{
		{Seq: 11, UserID: "u1", ItemID: "a", ChangeType: "updated"},
		{Seq: 12, UserID: "u1", ItemID: "b", ChangeType: "updated"},
	}

	processor := &mockProcessor{failSeq: 12}
	worker := NewChangeWorker(store, processor, &mockBootstrapper{}, nil, time.Second, "worker-1")
	err := worker.processOwner(context.Background(), "u1")
	if err == nil {
		t.Fatal("expected error")
	}

	if got := store.cursors["u1"].LastSeq; got != 11 {
		t.Fatalf("expected cursor to stop at 11, got %d", got)
	}
	if store.cursors["u1"].LastError == "" {
		t.Fatal("expected cursor error to be recorded")
	}
}

func TestChangeWorkerSkipsLockedOwner(t *testing.T) {
	store := newMockChangeStore()
	store.cursors["u1"] = &CursorState{OwnerUserID: "u1", LastSeq: 10, Initialized: true}
	store.changes["u1"] = []ChangeRecord{
		{Seq: 11, UserID: "u1", ItemID: "a", ChangeType: "updated"},
	}

	processor := &mockProcessor{}
	worker := NewChangeWorker(store, processor, &mockBootstrapper{}, &mockLocker{locked: map[string]bool{"u1": true}}, time.Second, "worker-1")
	if err := worker.processOwner(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}

	if got := store.cursors["u1"].LastSeq; got != 10 {
		t.Fatalf("expected cursor unchanged, got %d", got)
	}
	if len(processor.processed) != 0 {
		t.Fatalf("expected no processed changes, got %d", len(processor.processed))
	}
}

func TestChangeWorkerRunOnceRefreshesSnapshot(t *testing.T) {
	store := newMockChangeStore()
	store.backlogStats = BacklogStats{
		OwnersWithBacklog: 3,
		OldestPendingAtMs: time.Now().Add(-2 * time.Minute).UnixMilli(),
		StuckOwners:       1,
	}

	worker := NewChangeWorker(store, &mockProcessor{}, &mockBootstrapper{}, nil, time.Second, "worker-1")
	worker.runOnce(context.Background())

	snapshot := worker.Snapshot()
	if snapshot.OwnersWithBacklog != 3 {
		t.Fatalf("expected backlog owners 3, got %d", snapshot.OwnersWithBacklog)
	}
	if snapshot.StuckOwners != 1 {
		t.Fatalf("expected stuck owners 1, got %d", snapshot.StuckOwners)
	}
	if snapshot.OldestPendingAgeMs <= 0 {
		t.Fatalf("expected positive oldest pending age, got %d", snapshot.OldestPendingAgeMs)
	}
}

func TestVaultChangeProcessorMapsDeleteAction(t *testing.T) {
	handler := &mockHandler{}
	processor := NewVaultChangeProcessor(handler)

	err := processor.ProcessChange(context.Background(), ChangeRecord{
		Seq:        9,
		UserID:     "u1",
		ItemID:     "item-1",
		ChangeType: "deleted",
		CreatedAt:  123,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(handler.events) != 1 {
		t.Fatalf("expected one event, got %d", len(handler.events))
	}
	if handler.events[0].Action != "delete" {
		t.Fatalf("expected delete action, got %s", handler.events[0].Action)
	}
}
