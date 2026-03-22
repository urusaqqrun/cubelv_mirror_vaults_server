package sync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	defaultOwnerScanLimit  = 128
	defaultChangeBatchSize = 100
	defaultCursorLeaseTTL  = 2 * time.Minute
)

// ChangeRecord 是從 sync_changes 讀出的單一變更。
type ChangeRecord struct {
	Seq        int
	UserID     string
	ItemID     string
	ChangeType string
	CreatedAt  int64
}

// CursorState 是 mirror_sync_cursor 的目前狀態。
type CursorState struct {
	OwnerUserID  string
	LastSeq      int
	LeaseOwner   string
	LeaseUntilMs int64
	LastError    string
	UpdatedAtMs  int64
	Initialized  bool
}

// BacklogStats 是同步 backlog 的摘要。
type BacklogStats struct {
	OwnersWithBacklog int
	OldestPendingAtMs int64
	StuckOwners       int
}

// ChangeLogStore 定義 worker 所需的持久化能力。
type ChangeLogStore interface {
	ListOwnersForSync(ctx context.Context, limit int) ([]string, error)
	AcquireCursorLease(ctx context.Context, ownerUserID, leaseOwner string, leaseTTL time.Duration) (*CursorState, bool, error)
	ReleaseCursorLease(ctx context.Context, ownerUserID, leaseOwner string) error
	GetLatestSeq(ctx context.Context, userID string) (int, error)
	GetChangesAfterSeq(ctx context.Context, userID string, afterSeq, limit int) ([]ChangeRecord, error)
	MarkCursorInitialized(ctx context.Context, ownerUserID, leaseOwner string, seq int) error
	AdvanceCursor(ctx context.Context, ownerUserID, leaseOwner string, seq int) error
	RecordCursorError(ctx context.Context, ownerUserID, leaseOwner, message string) error
	GetBacklogStats(ctx context.Context) (BacklogStats, error)
}

// ChangeProcessor 將 change log 記錄投影到 Vault。
type ChangeProcessor interface {
	ProcessChange(ctx context.Context, change ChangeRecord) error
}

// Bootstrapper 在首次建立 cursor 時做全量初始化。
type Bootstrapper interface {
	BootstrapOwner(ctx context.Context, ownerUserID string) error
}

// WorkerHealthSnapshot 提供健康檢查與觀測用的摘要。
type WorkerHealthSnapshot struct {
	Running              bool      `json:"running"`
	LeaseOwner           string    `json:"leaseOwner"`
	LastLoopAt           time.Time `json:"lastLoopAt,omitempty"`
	LastSuccessfulSyncAt time.Time `json:"lastSuccessfulSyncAt,omitempty"`
	LastProcessedOwner   string    `json:"lastProcessedOwner,omitempty"`
	LastProcessedSeq     int       `json:"lastProcessedSeq,omitempty"`
	LastError            string    `json:"lastError,omitempty"`
	OwnersWithBacklog    int       `json:"ownersWithBacklog"`
	OldestPendingAgeMs   int64     `json:"oldestPendingAgeMs"`
	StuckOwners          int       `json:"stuckOwners"`
}

// ChangeWorker 以持久化 cursor 順序消化 sync_changes。
type ChangeWorker struct {
	store        ChangeLogStore
	processor    ChangeProcessor
	bootstrapper Bootstrapper
	locker       VaultLocker

	interval        time.Duration
	leaseTTL        time.Duration
	ownerScanLimit  int
	changeBatchSize int
	leaseOwner      string

	mu       sync.RWMutex
	snapshot WorkerHealthSnapshot
}

func NewChangeWorker(store ChangeLogStore, processor ChangeProcessor, bootstrapper Bootstrapper, locker VaultLocker, interval time.Duration, leaseOwner string) *ChangeWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if leaseOwner == "" {
		leaseOwner = "mirror-worker"
	}
	return &ChangeWorker{
		store:           store,
		processor:       processor,
		bootstrapper:    bootstrapper,
		locker:          locker,
		interval:        interval,
		leaseTTL:        defaultCursorLeaseTTL,
		ownerScanLimit:  defaultOwnerScanLimit,
		changeBatchSize: defaultChangeBatchSize,
		leaseOwner:      leaseOwner,
		snapshot: WorkerHealthSnapshot{
			LeaseOwner: leaseOwner,
		},
	}
}

func (w *ChangeWorker) SetLeaseTTL(leaseTTL time.Duration) {
	if leaseTTL <= 0 {
		return
	}
	w.leaseTTL = leaseTTL
}

func (w *ChangeWorker) SetOwnerScanLimit(limit int) {
	if limit <= 0 {
		return
	}
	w.ownerScanLimit = limit
}

func (w *ChangeWorker) SetChangeBatchSize(limit int) {
	if limit <= 0 {
		return
	}
	w.changeBatchSize = limit
}

// Snapshot 回傳目前 worker 狀態的快照。
func (w *ChangeWorker) Snapshot() WorkerHealthSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot
}

// Start 啟動主同步迴圈。
func (w *ChangeWorker) Start(ctx context.Context) {
	w.setRunning(true)
	defer w.setRunning(false)

	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *ChangeWorker) runOnce(ctx context.Context) {
	w.updateSnapshot(func(s *WorkerHealthSnapshot) {
		s.LastLoopAt = time.Now()
	})

	if err := w.refreshBacklogStats(ctx); err != nil {
		w.setLastError(fmt.Sprintf("refresh backlog stats: %v", err))
		log.Printf("[ChangeWorker] refresh backlog stats error: %v", err)
	}

	owners, err := w.store.ListOwnersForSync(ctx, w.ownerScanLimit)
	if err != nil {
		w.setLastError(fmt.Sprintf("list owners for sync: %v", err))
		log.Printf("[ChangeWorker] list owners for sync error: %v", err)
		return
	}

	for _, ownerUserID := range owners {
		if ctx.Err() != nil {
			return
		}
		if err := w.processOwner(ctx, ownerUserID); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[ChangeWorker] process owner %s error: %v", ownerUserID, err)
		}
	}
}

func (w *ChangeWorker) processOwner(ctx context.Context, ownerUserID string) error {
	cursor, acquired, err := w.store.AcquireCursorLease(ctx, ownerUserID, w.leaseOwner, w.leaseTTL)
	if err != nil {
		w.setLastError(fmt.Sprintf("acquire lease owner=%s: %v", ownerUserID, err))
		return err
	}
	if !acquired || cursor == nil {
		return nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.store.ReleaseCursorLease(releaseCtx, ownerUserID, w.leaseOwner); err != nil {
			log.Printf("[ChangeWorker] release lease owner=%s error: %v", ownerUserID, err)
		}
	}()

	if w.locker != nil && w.locker.IsLocked(ownerUserID) {
		return nil
	}

	if !cursor.Initialized {
		return w.bootstrapOwner(ctx, ownerUserID)
	}

	changes, err := w.store.GetChangesAfterSeq(ctx, ownerUserID, cursor.LastSeq, w.changeBatchSize)
	if err != nil {
		w.setCursorError(ctx, ownerUserID, fmt.Sprintf("read changes after seq=%d: %v", cursor.LastSeq, err))
		return err
	}
	if len(changes) == 0 {
		return nil
	}

	for _, change := range changes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.locker != nil && w.locker.IsLocked(ownerUserID) {
			return nil
		}

		startedAt := time.Now()
		if err := w.processor.ProcessChange(ctx, change); err != nil {
			w.setCursorError(ctx, ownerUserID, fmt.Sprintf("process seq=%d item=%s: %v", change.Seq, change.ItemID, err))
			return err
		}
		if err := w.store.AdvanceCursor(ctx, ownerUserID, w.leaseOwner, change.Seq); err != nil {
			w.setCursorError(ctx, ownerUserID, fmt.Sprintf("advance cursor seq=%d: %v", change.Seq, err))
			return err
		}

		durationMs := time.Since(startedAt).Milliseconds()
		log.Printf("[ChangeWorker] owner=%s seq=%d change=%s item=%s duration_ms=%d",
			ownerUserID, change.Seq, change.ChangeType, change.ItemID, durationMs)
		w.updateSnapshot(func(s *WorkerHealthSnapshot) {
			s.LastSuccessfulSyncAt = time.Now()
			s.LastProcessedOwner = ownerUserID
			s.LastProcessedSeq = change.Seq
			s.LastError = ""
		})
	}

	return nil
}

func (w *ChangeWorker) bootstrapOwner(ctx context.Context, ownerUserID string) error {
	if w.bootstrapper == nil {
		return fmt.Errorf("bootstrapper is nil")
	}

	bootstrapSeq, err := w.store.GetLatestSeq(ctx, ownerUserID)
	if err != nil {
		w.setCursorError(ctx, ownerUserID, fmt.Sprintf("read latest seq for bootstrap: %v", err))
		return err
	}
	if err := w.bootstrapper.BootstrapOwner(ctx, ownerUserID); err != nil {
		w.setCursorError(ctx, ownerUserID, fmt.Sprintf("bootstrap owner: %v", err))
		return err
	}
	if err := w.store.MarkCursorInitialized(ctx, ownerUserID, w.leaseOwner, bootstrapSeq); err != nil {
		w.setCursorError(ctx, ownerUserID, fmt.Sprintf("mark cursor initialized seq=%d: %v", bootstrapSeq, err))
		return err
	}

	log.Printf("[ChangeWorker] bootstrap completed owner=%s seq=%d", ownerUserID, bootstrapSeq)
	w.updateSnapshot(func(s *WorkerHealthSnapshot) {
		s.LastSuccessfulSyncAt = time.Now()
		s.LastProcessedOwner = ownerUserID
		s.LastProcessedSeq = bootstrapSeq
		s.LastError = ""
	})
	return nil
}

func (w *ChangeWorker) refreshBacklogStats(ctx context.Context) error {
	stats, err := w.store.GetBacklogStats(ctx)
	if err != nil {
		return err
	}

	oldestAgeMs := int64(0)
	if stats.OldestPendingAtMs > 0 {
		oldestAgeMs = time.Now().UnixMilli() - stats.OldestPendingAtMs
		if oldestAgeMs < 0 {
			oldestAgeMs = 0
		}
	}

	w.updateSnapshot(func(s *WorkerHealthSnapshot) {
		s.OwnersWithBacklog = stats.OwnersWithBacklog
		s.OldestPendingAgeMs = oldestAgeMs
		s.StuckOwners = stats.StuckOwners
	})
	return nil
}

func (w *ChangeWorker) setCursorError(ctx context.Context, ownerUserID, message string) {
	if err := w.store.RecordCursorError(ctx, ownerUserID, w.leaseOwner, message); err != nil {
		log.Printf("[ChangeWorker] record cursor error owner=%s failed: %v", ownerUserID, err)
	}
	w.setLastError(message)
}

func (w *ChangeWorker) setLastError(message string) {
	w.updateSnapshot(func(s *WorkerHealthSnapshot) {
		s.LastError = message
	})
}

func (w *ChangeWorker) setRunning(running bool) {
	w.updateSnapshot(func(s *WorkerHealthSnapshot) {
		s.Running = running
	})
}

func (w *ChangeWorker) updateSnapshot(fn func(snapshot *WorkerHealthSnapshot)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn(&w.snapshot)
}
