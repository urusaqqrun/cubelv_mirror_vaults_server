package sync

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
	"github.com/urusaqqrun/vault-mirror-service/model"
	"golang.org/x/sync/errgroup"
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

	// Phase 1: pre-create all parent directories (serial, fast)
	for _, item := range sorted {
		if item == nil {
			continue
		}
		parentDir := exporter.ResolveParentDir(userID, item.GetParentID(), item.Type)
		_ = fs.MkdirAll(parentDir)
	}

	// Phase 2: write all .json files in parallel (20 goroutines)
	var itemCount int64
	var skippedCount int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(20)
	for _, item := range sorted {
		if item == nil {
			continue
		}
		item := item
		g.Go(func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			if _, err := exporter.ExportItem(userID, item); err != nil {
				log.Printf("[FullExport] %s %s error: %v", item.Type, item.ID, err)
				atomic.AddInt64(&skippedCount, 1)
				return nil // don't abort other items
			}
			atomic.AddInt64(&itemCount, 1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if err := fs.WriteFile(userID+"/.vault_initialized", []byte("1")); err != nil {
		log.Printf("[FullExport] write .vault_initialized error: %v", err)
	}

	log.Printf("[FullExport] 用戶 %s 匯出完成: %d items (skipped: %d)",
		userID, atomic.LoadInt64(&itemCount), atomic.LoadInt64(&skippedCount))
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
