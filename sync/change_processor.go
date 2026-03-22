package sync

import "context"

// VaultChangeProcessor 負責把 change log 記錄轉成 Vault projector 可理解的事件。
type VaultChangeProcessor struct {
	handler EventHandler
}

func NewVaultChangeProcessor(handler EventHandler) *VaultChangeProcessor {
	return &VaultChangeProcessor{handler: handler}
}

func (p *VaultChangeProcessor) ProcessChange(ctx context.Context, change ChangeRecord) error {
	action := "update"
	if change.ChangeType == "deleted" {
		action = "delete"
	}

	return p.handler.HandleEvent(ctx, SyncEvent{
		Collection: "item",
		UserID:     change.UserID,
		DocID:      change.ItemID,
		Action:     action,
		Timestamp:  change.CreatedAt,
		USN:        change.Seq,
	})
}
