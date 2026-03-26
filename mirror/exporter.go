package mirror

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/urusaqqrun/vault-mirror-service/model"
	"golang.org/x/sync/errgroup"
)

// Exporter 負責資料庫資料 → Vault 檔案的匯出
type Exporter struct {
	fs       VaultFS
	resolver *PathResolver

	// docPathIndex 快取 docID -> 路徑，避免每次匯出都全量 walk EFS
	indexMu      sync.RWMutex
	indexBuild   sync.Mutex
	indexUserID  string
	docPathIndex map[string]string
}

func NewExporter(fs VaultFS, resolver *PathResolver) *Exporter {
	return &Exporter{fs: fs, resolver: resolver}
}

// ExportItemResult ExportItem 的回傳結果
type ExportItemResult struct {
	Path     string // 實際寫入的完整路徑
	IsFolder bool
}

// ExportItem 通用匯出：每個 item 都對應一個 name.json。
func (e *Exporter) ExportItem(userId string, item *model.Item) (ExportItemResult, error) {
	mirrorData := ItemToMirrorData(item)
	parentDirPath := e.ResolveParentDir(userId, item.GetParentID(), item.Type)
	if err := e.fs.MkdirAll(parentDirPath); err != nil {
		return ExportItemResult{}, fmt.Errorf("mkdir parent: %w", err)
	}

	fileName := sanitizeName(mirrorData.Name) + ".json"
	fullPath := filepath.Join(parentDirPath, fileName)

	// 檔名衝突處理：同路徑已存在且 ID 不同 → 加 ID 後綴。
	fullPath = e.resolveCollision(fullPath, mirrorData.ID)

	e.cleanupOldItemPath(userId, mirrorData.ID, fullPath)

	jsonBytes, err := ItemToMirrorJSON(mirrorData)
	if err != nil {
		return ExportItemResult{}, fmt.Errorf("marshal item json: %w", err)
	}

	if err := e.fs.WriteFile(fullPath, jsonBytes); err != nil {
		return ExportItemResult{}, err
	}

	e.setIndexedPath(userId, mirrorData.ID, fullPath)
	return ExportItemResult{
		Path:     fullPath,
		IsFolder: model.IsFolder(item.Type),
	}, nil
}

// ResolveParentDir returns the directory path where an item's .json should be written.
func (e *Exporter) ResolveParentDir(userID, parentID, itemType string) string {
	if parentID == "" {
		return filepath.Join(userID, resolveTypeFromItemType(itemType))
	}

	resolvedPath, err := e.resolver.ResolvePath(parentID)
	if err != nil || resolvedPath == "" {
		return filepath.Join(userID, "_unsorted")
	}
	return e.resolveIndexedParentDir(userID, parentID, filepath.Join(userID, resolvedPath))
}

func (e *Exporter) resolveIndexedParentDir(userID, parentID, fallbackPath string) string {
	if parentID == "" {
		return fallbackPath
	}
	e.ensureDocPathIndex(userID)
	if indexed := e.getIndexedPath(userID, parentID); indexed != "" {
		return strings.TrimSuffix(indexed, ".json")
	}
	return fallbackPath
}

// resolveCollision 若目標路徑已被不同 ID 佔用，加 _{id後8碼} 後綴。
// 支援迴圈檢查：若後綴路徑也被佔用，改用完整 ID 作為後綴。
func (e *Exporter) resolveCollision(targetPath, itemID string) string {
	if !e.fs.Exists(targetPath) {
		return targetPath
	}
	existing, err := e.fs.ReadFile(targetPath)
	if err != nil {
		return targetPath
	}
	parsed, err := MirrorJSONToItem(existing)
	if err != nil || parsed.ID == itemID {
		return targetPath
	}

	ext := filepath.Ext(targetPath)
	base := strings.TrimSuffix(targetPath, ext)

	// 先嘗試短後綴（ID 後 8 碼）
	suffix := itemID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	candidate := base + "_" + suffix + ext
	if !e.fs.Exists(candidate) {
		return candidate
	}
	if data, err := e.fs.ReadFile(candidate); err == nil {
		if p, err := MirrorJSONToItem(data); err == nil && p.ID == itemID {
			return candidate
		}
	}

	// 短後綴也被佔用 → 使用完整 ID
	candidate = base + "_" + itemID + ext
	return candidate
}

func collisionSuffix(itemID string) string {
	suffix := itemID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return suffix
}

// cleanupOldItemPath 清理同 ID 但舊位置的投影（改名/搬移情境）。
func (e *Exporter) cleanupOldItemPath(userID, docID, newPath string) {
	e.ensureDocPathIndex(userID)
	oldPath := e.getIndexedPath(userID, docID)
	if oldPath == "" || oldPath == newPath {
		return
	}

	_ = e.fs.Remove(oldPath)

	oldDir := strings.TrimSuffix(oldPath, ".json")
	newDir := strings.TrimSuffix(newPath, ".json")
	if oldDir == newDir || !e.fs.Exists(oldDir) {
		return
	}
	if !e.fs.Exists(newDir) {
		if err := e.fs.Rename(oldDir, newDir); err == nil {
			return
		}
	}
	_ = e.fs.RemoveAll(oldDir)
}

// DeleteItem 通用刪除：刪除 item 的 .json 與同名子目錄。
func (e *Exporter) DeleteItem(userId, itemID string) error {
	e.ensureDocPathIndex(userId)
	oldPath := e.getIndexedPath(userId, itemID)
	if oldPath == "" {
		return nil
	}

	if e.fs.Exists(oldPath) {
		if err := e.fs.Remove(oldPath); err != nil {
			return err
		}
	}
	dirPath := strings.TrimSuffix(oldPath, ".json")
	if dirPath != oldPath && e.fs.Exists(dirPath) {
		if err := e.fs.RemoveAll(dirPath); err != nil {
			return err
		}
	}
	e.removeIndexedPath(userId, itemID)
	return nil
}

// ExportBatch 使用 errgroup 並行寫入多個檔案到 EFS（上限 8 goroutines）
// 所有 item 統一走 ExportItem。
func (e *Exporter) ExportBatch(userId string, items []*model.Item) error {
	g := new(errgroup.Group)
	g.SetLimit(8)
	for _, item := range items {
		item := item
		g.Go(func() error {
			if item == nil {
				return nil
			}
			_, err := e.ExportItem(userId, item)
			return err
		})
	}
	return g.Wait()
}

func (e *Exporter) ensureDocPathIndex(userID string) {
	e.indexMu.RLock()
	if e.docPathIndex != nil && e.indexUserID == userID {
		e.indexMu.RUnlock()
		return
	}
	e.indexMu.RUnlock()

	e.indexBuild.Lock()
	defer e.indexBuild.Unlock()

	e.indexMu.RLock()
	if e.docPathIndex != nil && e.indexUserID == userID {
		e.indexMu.RUnlock()
		return
	}
	e.indexMu.RUnlock()

	next := make(map[string]string)
	e.fs.Walk(userID, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		data, rErr := e.fs.ReadFile(path)
		if rErr != nil {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			if mirrorItem, err := MirrorJSONToItem(data); err == nil {
				next[mirrorItem.ID] = path
				return nil
			}
			var payload map[string]interface{}
			if uErr := json.Unmarshal(data, &payload); uErr == nil {
				if id, ok := payload["id"].(string); ok && id != "" {
					next[id] = path
				}
			}
		}
		return nil
	})

	e.indexMu.Lock()
	e.docPathIndex = next
	e.indexUserID = userID
	e.indexMu.Unlock()
}

func (e *Exporter) getIndexedPath(userID, docID string) string {
	e.indexMu.RLock()
	defer e.indexMu.RUnlock()
	if e.indexUserID != userID || e.docPathIndex == nil {
		return ""
	}
	return e.docPathIndex[docID]
}

func (e *Exporter) setIndexedPath(userID, docID, path string) {
	e.ensureDocPathIndex(userID)
	e.indexMu.Lock()
	defer e.indexMu.Unlock()
	if e.indexUserID != userID || e.docPathIndex == nil {
		return
	}
	e.docPathIndex[docID] = path
}

func (e *Exporter) removeIndexedPath(userID, docID string) {
	e.ensureDocPathIndex(userID)
	e.indexMu.Lock()
	defer e.indexMu.Unlock()
	if e.indexUserID != userID || e.docPathIndex == nil {
		return
	}
	delete(e.docPathIndex, docID)
}
