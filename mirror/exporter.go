package mirror

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
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

// ExportFolder 舊介面轉為統一 Item JSON 匯出。
func (e *Exporter) ExportFolder(userId string, meta FolderMeta) error {
	_, err := e.ExportItem(userId, folderMetaToItem(meta))
	return err
}

// ExportNote 舊介面轉為統一 Item JSON 匯出。
func (e *Exporter) ExportNote(userId string, meta NoteMeta, html string) error {
	_, err := e.ExportItem(userId, noteMetaToItem(meta, html))
	return err
}

// ExportCard 舊介面轉為統一 Item JSON 匯出。
func (e *Exporter) ExportCard(userId string, meta CardMeta) error {
	_, err := e.ExportItem(userId, cardMetaToItem(meta, "CARD"))
	return err
}

// ExportChart 舊介面轉為統一 Item JSON 匯出。
func (e *Exporter) ExportChart(userId string, meta CardMeta) error {
	_, err := e.ExportItem(userId, cardMetaToItem(meta, "CHART"))
	return err
}

// ExportItemResult ExportItem 的回傳結果
type ExportItemResult struct {
	Path     string // 實際寫入的完整路徑
	IsFolder bool
}

// ExportItem 通用匯出：每個 item 都對應一個 name.json。
func (e *Exporter) ExportItem(userId string, item *model.Item) (ExportItemResult, error) {
	mirrorData := ItemToMirrorData(item)
	parentDirPath := e.resolveParentDir(userId, item.GetParentID(), item.Type)
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

func (e *Exporter) resolveParentDir(userID, parentID, itemType string) string {
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

// DeleteFolder 舊介面轉為通用刪除。
func (e *Exporter) DeleteFolder(userId string, folderID string) error {
	return e.DeleteItem(userId, folderID)
}

// DeleteNote 舊介面轉為通用刪除。
func (e *Exporter) DeleteNote(userId string, noteID string, title string, parentFolderID string) error {
	return e.DeleteItem(userId, noteID)
}

// ExportBatchEntry 批次匯出項目
type ExportBatchEntry struct {
	ItemType   string
	FolderMeta *FolderMeta
	NoteMeta   *NoteMeta
	NoteHTML   string
	CardMeta   *CardMeta
	Item       *model.Item // 新格式：通用 Item 匯出
}

// ExportBatch 使用 errgroup 並行寫入多個檔案到 EFS（上限 8 goroutines）
func (e *Exporter) ExportBatch(userId string, entries []ExportBatchEntry) error {
	g := new(errgroup.Group)
	g.SetLimit(8)
	for _, entry := range entries {
		entry := entry
		g.Go(func() error {
			if entry.Item != nil {
				_, err := e.ExportItem(userId, entry.Item)
				return err
			}
			switch {
			case model.IsFolder(entry.ItemType):
				if entry.FolderMeta == nil {
					return nil
				}
				return e.ExportFolder(userId, *entry.FolderMeta)
			case entry.ItemType == "NOTE" || entry.ItemType == "TODO":
				if entry.NoteMeta == nil {
					return nil
				}
				return e.ExportNote(userId, *entry.NoteMeta, entry.NoteHTML)
			case entry.ItemType == "CARD":
				if entry.CardMeta == nil {
					return nil
				}
				return e.ExportCard(userId, *entry.CardMeta)
			case entry.ItemType == "CHART":
				if entry.CardMeta == nil {
					return nil
				}
				return e.ExportChart(userId, *entry.CardMeta)
			default:
				log.Printf("[ExportBatch] unknown itemType: %s", entry.ItemType)
				return nil
			}
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

func folderMetaToItem(meta FolderMeta) *model.Item {
	fields := map[string]interface{}{
		"folderName": meta.FolderName,
		"noteNum":    meta.NoteNum,
		"isTemp":     meta.IsTemp,
	}
	itemType := "NOTE_FOLDER"
	if meta.Type != nil && *meta.Type != "" {
		itemType = resolveTypeFromItemType(*meta.Type) + "_FOLDER"
		fields["folderType"] = *meta.Type
	}
	if meta.ParentID != nil {
		fields["parentID"] = *meta.ParentID
	}
	if meta.OrderAt != nil {
		fields["orderAt"] = *meta.OrderAt
	}
	if meta.Icon != nil {
		fields["icon"] = *meta.Icon
	}
	if meta.CreatedAt != "" {
		fields["createdAt"] = meta.CreatedAt
	}
	if meta.UpdatedAt != "" {
		fields["updatedAt"] = meta.UpdatedAt
	}
	fields["usn"] = meta.USN
	return &model.Item{
		ID:     meta.ID,
		Name:   meta.FolderName,
		Type:   itemType,
		Fields: fields,
	}
}

func noteMetaToItem(meta NoteMeta, html string) *model.Item {
	itemType := meta.Type
	if itemType == "" {
		itemType = "NOTE"
	}
	fields := map[string]interface{}{
		"title":     meta.Title,
		"name":      meta.Title,
		"parentID":  meta.ParentID,
		"tags":      meta.Tags,
		"createdAt": meta.CreatedAt,
		"updatedAt": meta.UpdatedAt,
		"isNew":     meta.IsNew,
		"usn":       meta.USN,
	}
	if html != "" {
		fields["content"] = html
	}
	if meta.OrderAt != "" {
		fields["orderAt"] = meta.OrderAt
	}
	if meta.Status != "" {
		fields["status"] = meta.Status
	}
	if meta.AiTitle != "" {
		fields["aiTitle"] = meta.AiTitle
	}
	if meta.AiTags != nil {
		fields["aiTags"] = meta.AiTags
	}
	if meta.ImgURLs != nil {
		fields["imgURLs"] = meta.ImgURLs
	}
	return &model.Item{
		ID:     meta.ID,
		Name:   meta.Title,
		Type:   itemType,
		Fields: fields,
	}
}

func cardMetaToItem(meta CardMeta, itemType string) *model.Item {
	if itemType == "" {
		itemType = "CARD"
	}
	fields := map[string]interface{}{
		"parentID": meta.ParentID,
		"name":     meta.Name,
		"usn":      meta.USN,
	}
	if meta.OrderAt != nil {
		fields["orderAt"] = *meta.OrderAt
	}
	if meta.CreatedAt != "" {
		fields["createdAt"] = meta.CreatedAt
	}
	if meta.UpdatedAt != "" {
		fields["updatedAt"] = meta.UpdatedAt
	}
	if itemType == "CHART" {
		if meta.Fields != nil {
			fields["data"] = *meta.Fields
		}
	} else {
		if meta.Fields != nil {
			fields["fields"] = *meta.Fields
		}
		if meta.Reviews != nil {
			fields["reviews"] = *meta.Reviews
		}
		if meta.ContributorID != nil {
			fields["contributorId"] = *meta.ContributorID
		}
		if meta.Coordinates != nil {
			fields["coordinates"] = *meta.Coordinates
		}
		fields["isDeleted"] = meta.IsDeleted
	}
	return &model.Item{
		ID:     meta.ID,
		Name:   meta.Name,
		Type:   itemType,
		Fields: fields,
	}
}
