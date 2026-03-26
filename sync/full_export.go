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

	claudeMD := buildClaudeMD()
	if err := fs.WriteFile(userID+"/CLAUDE.md", []byte(claudeMD)); err != nil {
		log.Printf("[FullExport] write CLAUDE.md error: %v", err)
	}

	log.Printf("[FullExport] 用戶 %s 匯出完成: %d items (skipped: %d)",
		userID, itemCount, skippedCount)
	return nil
}

// buildClaudeMD 產生 CLAUDE.md 專案描述檔
func buildClaudeMD() string {
	return `# NoteCEO Vault

你是 NoteCEO Vault 的 AI 助手，正在操作一個包含用戶資料的檔案系統。

## 目錄結構

頂層目錄名稱對應 itemType（如 NOTE、CARD 等），由系統動態產生。
每個 item 都是 {name}.json，有子項就有同名目錄。

## 規則

1. 保留每個 .json 內的 id 與 parentID
2. 搬移 item 時更新 parentID
3. 改名 item 時同步調整 .json 與同名子目錄
4. 目錄只代表子項容器，不是另一份 metadata
`
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
