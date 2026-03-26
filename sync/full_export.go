package sync

import (
	"context"
	"fmt"
	"log"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
	"github.com/urusaqqrun/vault-mirror-service/model"
)

// FullExporter 全量匯出用戶 Vault 所需的讀取介面
type FullExporter interface {
	ListAllItems(ctx context.Context, userID string) ([]*model.Item, error)
}

// ExportFullVault 將用戶所有資料從 Item collection 匯出到 Vault 檔案系統
func ExportFullVault(ctx context.Context, fs mirror.VaultFS, reader FullExporter, userID string) error {
	log.Printf("[FullExport] 開始匯出用戶 %s 的 Vault", userID)

	if err := fs.RemoveAll(userID); err != nil {
		return fmt.Errorf("remove user root: %w", err)
	}
	if err := fs.MkdirAll(userID); err != nil {
		return fmt.Errorf("mkdir user root: %w", err)
	}

	allItems, err := reader.ListAllItems(ctx, userID)
	if err != nil {
		return fmt.Errorf("list all items: %w", err)
	}
	resolver := buildPathResolverFromItems(allItems)
	exporter := mirror.NewExporter(fs, resolver)
	sorted := topoSortItems(allItems)

	itemCount := 0
	skippedCount := 0
	for _, item := range sorted {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item == nil {
			continue
		}
		if _, err := exporter.ExportItem(userID, item); err != nil {
			log.Printf("[FullExport] %s %s error: %v", item.Type, item.ID, err)
			skippedCount++
			continue
		}
		itemCount++
	}

	if err := fs.WriteFile(userID+"/.vault_initialized", []byte("1")); err != nil {
		log.Printf("[FullExport] write .vault_initialized error: %v", err)
	}

	log.Printf("[FullExport] 用戶 %s 匯出完成: %d items (skipped: %d)",
		userID, itemCount, skippedCount)
	return nil
}

func topoSortItems(items []*model.Item) []*model.Item {
	index := make(map[string]*model.Item, len(items))
	for _, item := range items {
		if item == nil || item.ID == "" {
			continue
		}
		index[item.ID] = item
	}

	state := make(map[string]int, len(index))
	ordered := make([]*model.Item, 0, len(index))
	var visit func(item *model.Item)
	visit = func(item *model.Item) {
		if item == nil || item.ID == "" {
			return
		}
		switch state[item.ID] {
		case 1, 2:
			return
		}
		state[item.ID] = 1
		parentID := item.GetParentID()
		if parentID != "" {
			visit(index[parentID])
		}
		state[item.ID] = 2
		ordered = append(ordered, item)
	}

	for _, item := range items {
		visit(item)
	}
	return ordered
}
