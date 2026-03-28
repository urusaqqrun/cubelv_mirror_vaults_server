package sync

import "context"

// SyncEvent 是 Vault projector 使用的標準化事件。
type SyncEvent struct {
	Collection string
	UserID     string
	DocID      string
	Action     string
	Timestamp  int64
	Version    int
}

// EventHandler 是事件投影器的最小介面。
type EventHandler interface {
	HandleEvent(ctx context.Context, event SyncEvent) error
}
