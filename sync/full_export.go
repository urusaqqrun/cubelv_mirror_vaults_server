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
	ListFolders(ctx context.Context, userID string) ([]*model.Folder, error)
	ListAllNotes(ctx context.Context, userID string) ([]*model.Note, error)
	ListAllCards(ctx context.Context, userID string) ([]*model.Card, error)
	ListAllCharts(ctx context.Context, userID string) ([]*model.Chart, error)
}

// ExportFullVault 將用戶所有資料從 MongoDB 匯出到 Vault 檔案系統
func ExportFullVault(ctx context.Context, fs mirror.VaultFS, reader FullExporter, userID string) error {
	log.Printf("[FullExport] 開始匯出用戶 %s 的 Vault", userID)

	// 建立根目錄
	fs.MkdirAll(userID)

	// 載入所有 Folder 建立 PathResolver
	folders, err := reader.ListFolders(ctx, userID)
	if err != nil {
		return fmt.Errorf("list folders: %w", err)
	}
	resolver := buildPathResolver(folders)
	exporter := mirror.NewExporter(fs, resolver)

	// 匯出所有 Folder
	for _, f := range folders {
		if f == nil {
			continue
		}
		if err := exporter.ExportFolder(userID, toFolderMeta(f)); err != nil {
			log.Printf("[FullExport] folder %s error: %v", f.ID, err)
		}
	}

	// 匯出所有 Note
	notes, err := reader.ListAllNotes(ctx, userID)
	if err != nil {
		return fmt.Errorf("list notes: %w", err)
	}
	for _, n := range notes {
		if n == nil {
			continue
		}
		if err := exporter.ExportNote(userID, toNoteMeta(n), n.GetContent()); err != nil {
			log.Printf("[FullExport] note %s error: %v", n.ID, err)
		}
	}

	// 匯出所有 Card
	cards, err := reader.ListAllCards(ctx, userID)
	if err != nil {
		return fmt.Errorf("list cards: %w", err)
	}
	for _, c := range cards {
		if c == nil {
			continue
		}
		if err := exporter.ExportCard(userID, toCardMeta(c)); err != nil {
			log.Printf("[FullExport] card %s error: %v", c.ID, err)
		}
	}

	// 匯出所有 Chart
	charts, err := reader.ListAllCharts(ctx, userID)
	if err != nil {
		return fmt.Errorf("list charts: %w", err)
	}
	for _, c := range charts {
		if c == nil {
			continue
		}
		if err := exporter.ExportChart(userID, toChartMeta(c)); err != nil {
			log.Printf("[FullExport] chart %s error: %v", c.ID, err)
		}
	}

	// 寫入 CLAUDE.md
	claudeMD := buildClaudeMD()
	fs.WriteFile(userID+"/CLAUDE.md", []byte(claudeMD))

	log.Printf("[FullExport] 用戶 %s 匯出完成: %d folders, %d notes, %d cards, %d charts",
		userID, len(folders), len(notes), len(cards), len(charts))
	return nil
}

// buildClaudeMD 產生 CLAUDE.md 專案描述檔
func buildClaudeMD() string {
	return `# NoteCEO Vault

你是 NoteCEO Vault 的 AI 助手，正在操作一個包含用戶筆記、卡片、圖表的檔案系統。

## 目錄結構

- NOTE/  — 筆記資料夾（.md 檔案，含 YAML frontmatter）
- TODO/  — 待辦資料夾（.md 檔案，含 YAML frontmatter）
- CARD/  — 卡片畫廊（.json 檔案）
- CHART/ — 圖表（.json 檔案）

每個資料夾都有 _folder.json 存放元資料（ID, parentID, orderAt 等）。

## 規則

1. 不要刪除任何 _folder.json 中的 ID、memberID 欄位
2. 修改 .md 檔案時保留 frontmatter 的 id 和 parentID
3. 搬移檔案時更新 frontmatter 中的 parentID
4. 新建資料夾時必須建立 _folder.json（至少含 folderName 和 type）
5. orderAt 為時間戳字串，決定同層排序
`
}
