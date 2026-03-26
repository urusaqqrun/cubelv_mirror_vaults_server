package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
	"github.com/urusaqqrun/vault-mirror-service/model"
)

var (
	ErrVaultLocked = errors.New("user vault is locked by active AI task")
)

// VaultLocker 查詢 Vault 鎖定狀態（避免循環依賴 executor 套件）
type VaultLocker interface {
	IsLocked(userId string) bool
}

// DataReader 事件處理所需的資料讀取能力
// 由 database 層提供實作，測試可用 mock。
type DataReader interface {
	ListFolders(ctx context.Context, userID string) ([]*model.Folder, error)
	GetFolder(ctx context.Context, userID, folderID string) (*model.Folder, error)
	GetNote(ctx context.Context, userID, noteID string) (*model.Note, error)
	GetCard(ctx context.Context, userID, cardID string) (*model.Card, error)
	GetChart(ctx context.Context, userID, chartID string) (*model.Chart, error)
	GetItem(ctx context.Context, userID, itemID string) (*model.Item, error)
	ListAllItems(ctx context.Context, userID string) ([]*model.Item, error)
}

// resolverEntry PathResolver 快取項目
type resolverEntry struct {
	resolver  *mirror.PathResolver
	expiresAt time.Time
}

type docPathEntry struct {
	path     string
	isFolder bool
}

type docPathIndexEntry struct {
	entries   map[string]docPathEntry
	expiresAt time.Time
}

const resolverCacheTTL = 30 * time.Second
const docPathIndexTTL = 30 * time.Second

// SyncEventHandler 將同步事件轉為 Vault 匯出動作
type SyncEventHandler struct {
	fs     mirror.VaultFS
	reader DataReader
	locker VaultLocker // nil 時不檢查鎖定

	mu            sync.Mutex
	resolverCache map[string]*resolverEntry
	docPathIndex  map[string]*docPathIndexEntry
}

func NewSyncEventHandler(fs mirror.VaultFS, reader DataReader) *SyncEventHandler {
	return &SyncEventHandler{
		fs:            fs,
		reader:        reader,
		resolverCache: make(map[string]*resolverEntry),
		docPathIndex:  make(map[string]*docPathIndexEntry),
	}
}

// StartCacheEvictor 啟動定期清理過期快取的背景 goroutine，ctx 結束時自動停止
func (h *SyncEventHandler) StartCacheEvictor(ctx context.Context) {
	go h.evictExpiredCaches(ctx)
}

// SetLocker 設定 VaultLocker（在 main.go 組裝時呼叫）
func (h *SyncEventHandler) SetLocker(locker VaultLocker) {
	h.locker = locker
}

// evictExpiredCaches 定期清理過期的 resolverCache 和 docPathIndex，防止記憶體洩漏
func (h *SyncEventHandler) evictExpiredCaches(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			h.mu.Lock()
			for uid, entry := range h.resolverCache {
				if now.After(entry.expiresAt) {
					delete(h.resolverCache, uid)
				}
			}
			for uid, entry := range h.docPathIndex {
				if now.After(entry.expiresAt) {
					delete(h.docPathIndex, uid)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *SyncEventHandler) HandleEvent(ctx context.Context, event SyncEvent) error {
	if h.locker != nil && h.locker.IsLocked(event.UserID) {
		return ErrVaultLocked
	}

	col := strings.ToLower(event.Collection)

	if col == "item" {
		return h.handleItemEvent(ctx, event)
	}

	// 舊 collection 事件一律視為結構可能改動，直接失效快取。
	h.InvalidateResolver(event.UserID)
	h.InvalidateDocPathIndex(event.UserID)

	switch strings.ToLower(event.Action) {
	case "delete":
		return h.deleteByDocID(ctx, event.UserID, event.Collection, event.DocID)
	case "create", "update":
		return h.exportByDocID(ctx, event.UserID, event.Collection, event.DocID)
	default:
		return nil
	}
}

// handleItemEvent 處理 collection="item" 的統一事件
func (h *SyncEventHandler) handleItemEvent(ctx context.Context, event SyncEvent) error {
	action := strings.ToLower(event.Action)

	if action == "delete" {
		return h.deleteItemByDocID(ctx, event.UserID, event.DocID)
	}

	item, err := h.reader.GetItem(ctx, event.UserID, event.DocID)
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}

	h.InvalidateResolver(event.UserID)
	h.InvalidateDocPathIndex(event.UserID)

	resolver, err := h.getResolver(ctx, event.UserID)
	if err != nil {
		return err
	}
	exporter := mirror.NewExporter(h.fs, resolver)

	result, err := exporter.ExportItem(event.UserID, item)
	if err != nil {
		return err
	}

	h.setDocPath(event.UserID, item.ID, result.Path, result.IsFolder)
	return nil
}

// deleteItemByDocID 刪除 item 對應的 .json 與同名子目錄。
func (h *SyncEventHandler) deleteItemByDocID(ctx context.Context, userID, docID string) error {
	target, _, err := h.findPathByDocID(ctx, userID, docID)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	if h.fs.Exists(target) {
		if err := h.fs.Remove(target); err != nil {
			return err
		}
	}
	dirPath := strings.TrimSuffix(target, ".json")
	if dirPath != target && h.fs.Exists(dirPath) {
		if err := h.fs.RemoveAll(dirPath); err != nil {
			return err
		}
	}
	h.InvalidateResolver(userID)
	h.removeDocPath(userID, docID)
	return nil
}

// exportByDocID 處理舊 collection 事件，將舊 model 轉換為通用 Item 後匯出
func (h *SyncEventHandler) exportByDocID(ctx context.Context, userID, collection, docID string) error {
	resolver, err := h.getResolver(ctx, userID)
	if err != nil {
		return err
	}
	exporter := mirror.NewExporter(h.fs, resolver)

	var item *model.Item

	switch strings.ToLower(collection) {
	case "folder":
		f, err := h.reader.GetFolder(ctx, userID, docID)
		if err != nil || f == nil {
			return err
		}
		item = folderToItem(f)
	case "note":
		n, err := h.reader.GetNote(ctx, userID, docID)
		if err != nil || n == nil {
			return err
		}
		item = noteToItem(n)
	case "card":
		c, err := h.reader.GetCard(ctx, userID, docID)
		if err != nil || c == nil {
			return err
		}
		item = cardToItem(c)
	case "chart":
		c, err := h.reader.GetChart(ctx, userID, docID)
		if err != nil || c == nil {
			return err
		}
		item = chartToItem(c)
	default:
		return nil
	}

	if _, err := exporter.ExportItem(userID, item); err != nil {
		return err
	}
	if err := h.rebuildDocPathIndex(ctx, userID); err != nil {
		return err
	}
	return nil
}

func (h *SyncEventHandler) deleteByDocID(ctx context.Context, userID, collection, docID string) error {
	target, _, err := h.findPathByDocID(ctx, userID, docID)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	if h.fs.Exists(target) {
		if err := h.fs.Remove(target); err != nil {
			return err
		}
	}
	dirPath := strings.TrimSuffix(target, ".json")
	if dirPath != target && h.fs.Exists(dirPath) {
		if err := h.fs.RemoveAll(dirPath); err != nil {
			return err
		}
	}
	h.removeDocPath(userID, docID)
	return nil
}

// getResolver 取得或建立用戶的 PathResolver（帶 TTL 快取）
func (h *SyncEventHandler) getResolver(ctx context.Context, userID string) (*mirror.PathResolver, error) {
	h.mu.Lock()
	if entry, ok := h.resolverCache[userID]; ok && time.Now().Before(entry.expiresAt) {
		r := entry.resolver
		h.mu.Unlock()
		return r, nil
	}
	h.mu.Unlock()

	items, err := h.reader.ListAllItems(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list all items: %w", err)
	}
	resolver := buildPathResolverFromItems(items)

	h.mu.Lock()
	h.resolverCache[userID] = &resolverEntry{resolver: resolver, expiresAt: time.Now().Add(resolverCacheTTL)}
	h.mu.Unlock()

	return resolver, nil
}

// InvalidateResolver 強制清除指定用戶的 PathResolver 快取。
func (h *SyncEventHandler) InvalidateResolver(userID string) {
	h.mu.Lock()
	delete(h.resolverCache, userID)
	h.mu.Unlock()
}

// InvalidateDocPathIndex 清除指定用戶 docID->path 索引。
func (h *SyncEventHandler) InvalidateDocPathIndex(userID string) {
	h.mu.Lock()
	delete(h.docPathIndex, userID)
	h.mu.Unlock()
}

func (h *SyncEventHandler) setDocPath(userID, docID, path string, isFolder bool) {
	if userID == "" || docID == "" || path == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.docPathIndex[userID]
	if !ok || time.Now().After(entry.expiresAt) {
		entry = &docPathIndexEntry{
			entries:   make(map[string]docPathEntry),
			expiresAt: time.Now().Add(docPathIndexTTL),
		}
		h.docPathIndex[userID] = entry
	}
	entry.entries[docID] = docPathEntry{path: path, isFolder: isFolder}
}

func (h *SyncEventHandler) removeDocPath(userID, docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if entry, ok := h.docPathIndex[userID]; ok {
		delete(entry.entries, docID)
	}
}

func (h *SyncEventHandler) lookupDocPath(userID, docID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.docPathIndex[userID]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(h.docPathIndex, userID)
		return "", false
	}
	if p, ok := entry.entries[docID]; ok {
		return p.path, p.isFolder
	}
	return "", false
}

func (h *SyncEventHandler) rebuildDocPathIndex(ctx context.Context, userID string) error {
	next := make(map[string]docPathEntry)
	root := userID
	walkErr := h.fs.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		data, rErr := h.fs.ReadFile(path)
		if rErr != nil {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			if mirrorItem, err := mirror.MirrorJSONToItem(data); err == nil {
				next[mirrorItem.ID] = docPathEntry{path: path, isFolder: model.IsFolder(mirrorItem.ItemType)}
				return nil
			}
			var card map[string]any
			if jErr := json.Unmarshal(data, &card); jErr == nil {
				if id, ok := card["id"].(string); ok && id != "" {
					next[id] = docPathEntry{path: path, isFolder: false}
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	h.mu.Lock()
	h.docPathIndex[userID] = &docPathIndexEntry{
		entries:   next,
		expiresAt: time.Now().Add(docPathIndexTTL),
	}
	h.mu.Unlock()
	return nil
}

func (h *SyncEventHandler) findPathByDocID(ctx context.Context, userID, docID string) (string, bool, error) {
	if p, isFolder := h.lookupDocPath(userID, docID); p != "" {
		return p, isFolder, nil
	}
	if err := h.rebuildDocPathIndex(ctx, userID); err != nil {
		return "", false, err
	}
	p, isFolder := h.lookupDocPath(userID, docID)
	return p, isFolder, nil
}

func buildPathResolverFromItems(items []*model.Item) *mirror.PathResolver {
	nodes := make([]mirror.TreeNode, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		nodes = append(nodes, mirror.TreeNode{
			ID:       item.ID,
			Name:     item.GetName(),
			ItemType: item.Type,
			ParentID: model.StrPtrField(item.Fields, "parentID"),
		})
	}
	return mirror.NewPathResolver(nodes)
}

// folderToItem 將舊 Folder model 轉換為通用 Item
func folderToItem(f *model.Folder) *model.Item {
	folderType := "NOTE"
	if f.Type != nil && *f.Type != "" {
		folderType = *f.Type
	}
	itemType := folderType + "_FOLDER"

	fields := map[string]interface{}{
		"name":    f.FolderName,
		"noteNum": f.NoteNum,
		"isTemp":  f.IsTemp,
		"usn":     f.Usn,
	}
	if f.ParentID != nil {
		fields["parentID"] = *f.ParentID
	}
	if f.OrderAt != nil {
		fields["orderAt"] = *f.OrderAt
	}
	if f.Icon != nil {
		fields["icon"] = *f.Icon
	}
	if f.CreatedAt != "" {
		fields["createdAt"] = f.CreatedAt
	}
	if f.UpdatedAt != "" {
		fields["updatedAt"] = f.UpdatedAt
	}
	if f.FolderSummary != nil {
		fields["folderSummary"] = *f.FolderSummary
	}
	if f.AiFolderName != nil {
		fields["aiFolderName"] = *f.AiFolderName
	}
	if f.AiFolderSummary != nil {
		fields["aiFolderSummary"] = *f.AiFolderSummary
	}
	if f.AiInstruction != nil {
		fields["aiInstruction"] = *f.AiInstruction
	}
	fields["autoUpdateSummary"] = f.AutoUpdateSummary
	if f.TemplateHTML != nil {
		fields["templateHtml"] = *f.TemplateHTML
	}
	if f.TemplateCSS != nil {
		fields["templateCss"] = *f.TemplateCSS
	}
	if f.UIPrompt != nil {
		fields["uiPrompt"] = *f.UIPrompt
	}
	fields["isShared"] = f.IsShared
	fields["searchable"] = f.Searchable
	fields["allowContribute"] = f.AllowContribute
	if f.ChartKind != nil {
		fields["chartKind"] = *f.ChartKind
	}

	return &model.Item{
		ID:     f.ID,
		Name:   f.FolderName,
		Type:   itemType,
		Fields: fields,
	}
}

// noteToItem 將舊 Note model 轉換為通用 Item
func noteToItem(n *model.Note) *model.Item {
	itemType := "NOTE"
	if n.Type == "TODO" {
		itemType = "TODO"
	}
	fields := map[string]interface{}{
		"title":     n.GetTitle(),
		"name":      n.GetTitle(),
		"parentID":  n.ParentID,
		"tags":      n.Tags,
		"createdAt": fmt.Sprintf("%d", n.CreateAt),
		"updatedAt": fmt.Sprintf("%d", n.UpdateAt),
		"isNew":     n.IsNew,
		"usn":       n.Usn,
	}
	if n.Content != nil {
		fields["content"] = *n.Content
	}
	if n.OrderAt != nil {
		fields["orderAt"] = *n.OrderAt
	}
	if n.Status != nil {
		fields["status"] = *n.Status
	}
	if n.AiTitle != nil {
		fields["aiTitle"] = *n.AiTitle
	}
	if n.AiTags != nil {
		fields["aiTags"] = n.AiTags
	}
	if n.ImgURLs != nil {
		fields["imgURLs"] = n.ImgURLs
	}
	return &model.Item{
		ID:     n.ID,
		Name:   n.GetTitle(),
		Type:   itemType,
		Fields: fields,
	}
}

// cardToItem 將舊 Card model 轉換為通用 Item
func cardToItem(c *model.Card) *model.Item {
	fields := map[string]interface{}{
		"parentID":  c.ParentID,
		"name":      c.Name,
		"usn":       c.Usn,
		"isDeleted": c.IsDeleted,
	}
	if c.Fields != nil {
		fields["fields"] = *c.Fields
	}
	if c.Reviews != nil {
		fields["reviews"] = *c.Reviews
	}
	if c.ContributorID != nil {
		fields["contributorId"] = *c.ContributorID
	}
	if c.Coordinates != nil {
		fields["coordinates"] = *c.Coordinates
	}
	if c.OrderAt != nil {
		fields["orderAt"] = *c.OrderAt
	}
	if c.CreatedAt != "" {
		fields["createdAt"] = c.CreatedAt
	}
	if c.UpdatedAt != "" {
		fields["updatedAt"] = c.UpdatedAt
	}
	return &model.Item{
		ID:     c.ID,
		Name:   c.Name,
		Type:   "CARD",
		Fields: fields,
	}
}

// chartToItem 將舊 Chart model 轉換為通用 Item
func chartToItem(c *model.Chart) *model.Item {
	fields := map[string]interface{}{
		"parentID":  c.ParentID,
		"name":      c.Name,
		"usn":       c.Usn,
		"isDeleted": c.IsDeleted,
	}
	if c.Data != nil {
		fields["data"] = *c.Data
	}
	if c.CreatedAt != "" {
		fields["createdAt"] = c.CreatedAt
	}
	if c.UpdatedAt != "" {
		fields["updatedAt"] = c.UpdatedAt
	}
	return &model.Item{
		ID:     c.ID,
		Name:   c.Name,
		Type:   "CHART",
		Fields: fields,
	}
}
