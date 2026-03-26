package mirror

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// ImportAction 回寫動作類型
type ImportAction string

const (
	ImportActionCreate ImportAction = "create"
	ImportActionUpdate ImportAction = "update"
	ImportActionDelete ImportAction = "delete"
	ImportActionMove   ImportAction = "move"
	ImportActionSkip   ImportAction = "skip"
)

// ImportEntry 單筆回寫項目
type ImportEntry struct {
	Action     ImportAction
	Collection string // "item"（統一寫入 Item collection）
	ItemType   string // 從 JSON 內容的 itemType 欄位讀取
	Path       string
	OldPath    string // 搬移時的舊路徑
	DocID      string // 刪除時從 beforeIDMap 取得

	// 通用 Item JSON
	ItemData *ItemMirrorData
}

// Importer 負責 Vault 檔案 → 資料庫資料的匯入
type Importer struct {
	fs VaultFS
}

func NewImporter(fs VaultFS) *Importer {
	return &Importer{fs: fs}
}

// ProcessDiff 根據 VaultDiff 產生匯入動作清單
// beforeIDMap: path→docID 映射，用於解析已刪除檔案的 ID（刪除的檔案無法讀取）
func (imp *Importer) ProcessDiff(userId string, created, modified, deleted []string, moved []MovedFileEntry, beforeIDMap map[string]string) ([]ImportEntry, error) {
	var entries []ImportEntry

	for _, path := range created {
		entry, err := imp.parseFile(userId, path, ImportActionCreate)
		if err != nil {
			log.Printf("[Importer] skip created %s: %v", path, err)
			continue
		}
		entries = append(entries, entry)
	}

	for _, path := range modified {
		entry, err := imp.parseFile(userId, path, ImportActionUpdate)
		if err != nil {
			log.Printf("[Importer] skip modified %s: %v", path, err)
			continue
		}
		entries = append(entries, entry)
	}

	for _, path := range deleted {
		entry := ImportEntry{
			Action:     ImportActionDelete,
			Collection: "item",
			Path:       path,
		}
		if beforeIDMap != nil {
			if id, ok := beforeIDMap[path]; ok {
				entry.DocID = id
			}
		}
		entries = append(entries, entry)
	}

	for _, m := range moved {
		entry, err := imp.parseFile(userId, m.NewPath, ImportActionMove)
		if err != nil {
			log.Printf("[Importer] skip moved %s -> %s: %v", m.OldPath, m.NewPath, err)
			continue
		}
		entry.OldPath = m.OldPath
		entries = append(entries, entry)
	}

	return entries, nil
}

// parseFile 解析 Vault 中的 JSON 檔案，itemType 從 JSON 內容讀取
func (imp *Importer) parseFile(userId, path string, action ImportAction) (ImportEntry, error) {
	if !strings.HasSuffix(path, ".json") {
		return ImportEntry{}, fmt.Errorf("unsupported file format (only .json): %s", path)
	}

	fullPath := filepath.Join(userId, path)
	data, err := imp.fs.ReadFile(fullPath)
	if err != nil {
		return ImportEntry{}, fmt.Errorf("read %s: %w", fullPath, err)
	}

	mirrorItem, err := MirrorJSONToItem(data)
	if err != nil {
		return ImportEntry{}, fmt.Errorf("parse json %s: %w", path, err)
	}

	// vault fallback name 不回寫到 DB
	if IsVaultFallbackName(mirrorItem.Name, mirrorItem.ID) {
		mirrorItem.Name = ""
	}

	return ImportEntry{
		Action:     action,
		Collection: "item",
		ItemType:   mirrorItem.ItemType,
		Path:       path,
		DocID:      mirrorItem.ID,
		ItemData:   mirrorItem,
	}, nil
}

// MovedFileEntry 搬移的檔案
type MovedFileEntry struct {
	OldPath string
	NewPath string
}
