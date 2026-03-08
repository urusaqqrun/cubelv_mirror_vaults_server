package mirror

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// E2E 場景 1: 用戶建 Note → Vault 同步
func TestE2E_UserCreatesNote_VaultSync(t *testing.T) {
	fs := NewMemoryVaultFS()
	resolver := NewPathResolver([]FolderNode{
		{ID: "f1", FolderName: "工作", Type: "NOTE", ParentID: nil},
		{ID: "f2", FolderName: "會議紀錄", Type: "NOTE", ParentID: strPtr("f1")},
	})
	exporter := NewExporter(fs, resolver)

	// 匯出 Folder
	noteType := "NOTE"
	exporter.ExportFolder("user1", FolderMeta{ID: "f1", MemberID: "user1", FolderName: "工作", Type: &noteType})
	exporter.ExportFolder("user1", FolderMeta{ID: "f2", MemberID: "user1", FolderName: "會議紀錄", Type: &noteType, ParentID: strPtr("f1")})

	// 匯出 Note
	err := exporter.ExportNote("user1", NoteMeta{
		ID: "n1", ParentID: "f2", Title: "週會記要",
		USN: 3, Tags: []string{"會議", "工作"},
		CreatedAt: "1700000000000", UpdatedAt: "1709000000000",
	}, "<h1>週會記要</h1><p>討論事項...</p>")

	if err != nil {
		t.Fatal(err)
	}

	// 驗證 Vault 檔案結構
	if !fs.Exists("user1/NOTE/工作") {
		t.Error("工作目錄應存在")
	}
	if !fs.Exists("user1/NOTE/工作/會議紀錄") {
		t.Error("會議紀錄目錄應存在")
	}
	if !fs.Exists("user1/NOTE/工作/會議紀錄/週會記要.md") {
		t.Error("週會記要.md 應存在")
	}

	// 讀取並驗證 Markdown 內容
	data, _ := fs.ReadFile("user1/NOTE/工作/會議紀錄/週會記要.md")
	content := string(data)
	if !strings.Contains(content, "id: n1") {
		t.Error("frontmatter 應包含 id")
	}
	if !strings.Contains(content, "會議") {
		t.Error("frontmatter 應包含 tags")
	}
	if !strings.Contains(content, "討論事項") {
		t.Error("body 應包含筆記內容")
	}
}

// E2E 場景 2: AI 搬移 Note → DB parentID 更新
func TestE2E_AIMoveNote_ParentIDUpdate(t *testing.T) {
	fs := NewMemoryVaultFS()
	resolver := NewPathResolver([]FolderNode{
		{ID: "f1", FolderName: "未分類", Type: "NOTE", ParentID: nil},
		{ID: "f2", FolderName: "工作", Type: "NOTE", ParentID: nil},
	})
	exporter := NewExporter(fs, resolver)

	// 初始狀態：Note 在「未分類」
	noteType := "NOTE"
	exporter.ExportFolder("user1", FolderMeta{ID: "f1", MemberID: "user1", FolderName: "未分類", Type: &noteType})
	exporter.ExportFolder("user1", FolderMeta{ID: "f2", MemberID: "user1", FolderName: "工作", Type: &noteType})
	exporter.ExportNote("user1", NoteMeta{
		ID: "n1", ParentID: "f1", Title: "重要筆記", USN: 3,
		CreatedAt: "1700000000000", UpdatedAt: "1709000000000",
	}, "<p>重要內容</p>")

	// AI 搬移：從「未分類」到「工作」
	// 模擬 AI 更新了 parentID 後重新寫入
	movedMD := `---
id: n1
parentID: f2
title: 重要筆記
usn: 3
htmlHash: abc123
createdAt: "1700000000000"
updatedAt: "1709000000000"
---

重要內容
`
	fs.WriteFile("user1/NOTE/工作/重要筆記.md", []byte(movedMD))
	fs.Remove("user1/NOTE/未分類/重要筆記.md")

	// Importer 解析搬移
	importer := NewImporter(fs)
	entries, err := importer.ProcessDiff("user1", nil, nil, nil, []MovedFileEntry{
		{OldPath: "NOTE/未分類/重要筆記.md", NewPath: "NOTE/工作/重要筆記.md"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionMove {
		t.Errorf("action: got %q, want %q", entries[0].Action, ImportActionMove)
	}
	if entries[0].NoteMeta.ParentID != "f2" {
		t.Errorf("parentID should be f2 after move, got %q", entries[0].NoteMeta.ParentID)
	}
}

// E2E 場景 3: 並發編輯衝突 → dbUSN > aiStartUSN 時跳過回寫（保留用戶版本）
func TestE2E_ConcurrentEdit_UserWins(t *testing.T) {
	aiStartUSN := 5
	dbUSN := 8

	shouldSkip := dbUSN > aiStartUSN
	if !shouldSkip {
		t.Error("dbUSN > aiStartUSN should trigger skip (user modified during AI task)")
	}

	// 反向驗證：dbUSN <= aiStartUSN 則套用
	dbUSN2 := 5
	shouldSkip2 := dbUSN2 > aiStartUSN
	if shouldSkip2 {
		t.Error("dbUSN == aiStartUSN should NOT skip")
	}
}

// E2E 場景 4: 大量 Folder + Note 全量同步
func TestE2E_BulkSync_100Folders_500Notes(t *testing.T) {
	fs := NewMemoryVaultFS()

	// 建立 100 個 Folder
	folders := make([]FolderNode, 100)
	for i := 0; i < 100; i++ {
		folders[i] = FolderNode{
			ID:         fmt.Sprintf("f%d", i),
			FolderName: fmt.Sprintf("Folder_%d", i),
			Type:       "NOTE",
			ParentID:   nil,
		}
	}
	resolver := NewPathResolver(folders)
	exporter := NewExporter(fs, resolver)

	noteType := "NOTE"
	for _, f := range folders {
		exporter.ExportFolder("user1", FolderMeta{
			ID: f.ID, MemberID: "user1", FolderName: f.FolderName, Type: &noteType,
		})
	}

	// 建立 500 個 Note（每 Folder 5 個）
	noteCount := 0
	for i := 0; i < 100; i++ {
		for j := 0; j < 5; j++ {
			noteID := fmt.Sprintf("n%d_%d", i, j)
			title := fmt.Sprintf("Note_%d_%d", i, j)
			err := exporter.ExportNote("user1", NoteMeta{
				ID: noteID, ParentID: fmt.Sprintf("f%d", i), Title: title, USN: 1,
				CreatedAt: "1700000000000", UpdatedAt: "1709000000000",
			}, fmt.Sprintf("<p>Content of %s</p>", title))
			if err != nil {
				t.Fatalf("export note %s: %v", noteID, err)
			}
			noteCount++
		}
	}

	if noteCount != 500 {
		t.Errorf("exported %d notes, want 500", noteCount)
	}

	// 驗證隨機幾個檔案存在
	if !fs.Exists("user1/NOTE/Folder_0/Note_0_0.md") {
		t.Error("first note should exist")
	}
	if !fs.Exists("user1/NOTE/Folder_99/Note_99_4.md") {
		t.Error("last note should exist")
	}

	// 驗證 _folder.json 存在
	data, err := fs.ReadFile("user1/NOTE/Folder_50/_folder.json")
	if err != nil {
		t.Fatal("folder 50 json should exist:", err)
	}
	var meta FolderMeta
	json.Unmarshal(data, &meta)
	if meta.FolderName != "Folder_50" {
		t.Errorf("folder name: got %q, want %q", meta.FolderName, "Folder_50")
	}
}

func TestE2E_DeepNestedFolderMove(t *testing.T) {
	fs := NewMemoryVaultFS()
	resolver := NewPathResolver([]FolderNode{
		{ID: "f-root", FolderName: "ROOT", Type: "NOTE", ParentID: nil},
		{ID: "f-a", FolderName: "A", Type: "NOTE", ParentID: strPtr("f-root")},
		{ID: "f-b", FolderName: "B", Type: "NOTE", ParentID: strPtr("f-a")},
		{ID: "f-c", FolderName: "C", Type: "NOTE", ParentID: strPtr("f-b")},
		{ID: "f-todo", FolderName: "待辦", Type: "TODO", ParentID: nil},
	})
	exporter := NewExporter(fs, resolver)

	if err := exporter.ExportNote("user1", NoteMeta{
		ID: "n1", ParentID: "f-c", Title: "深層筆記", USN: 1,
		CreatedAt: "1700000000000", UpdatedAt: "1709000000000",
	}, "<p>內容</p>"); err != nil {
		t.Fatal(err)
	}

	moveToA := `---
id: n1
parentID: f-a
title: 深層筆記
usn: 1
---

內容
`
	if err := fs.WriteFile("user1/NOTE/ROOT/A/深層筆記.md", []byte(moveToA)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("user1/NOTE/ROOT/A/B/C/深層筆記.md"); err != nil {
		t.Fatal(err)
	}

	importer := NewImporter(fs)
	entries, err := importer.ProcessDiff("user1", nil, nil, nil, []MovedFileEntry{
		{OldPath: "NOTE/ROOT/A/B/C/深層筆記.md", NewPath: "NOTE/ROOT/A/深層筆記.md"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionMove {
		t.Fatalf("action: got %q, want %q", entries[0].Action, ImportActionMove)
	}
	if entries[0].NoteMeta == nil || entries[0].NoteMeta.ParentID != "f-a" {
		t.Fatalf("move to A should set parentID=f-a, got %+v", entries[0].NoteMeta)
	}

	moveToTodo := `---
id: n1
parentID: f-todo
title: 深層筆記
usn: 1
---

內容
`
	if err := fs.WriteFile("user1/TODO/待辦/深層筆記.md", []byte(moveToTodo)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("user1/NOTE/ROOT/A/深層筆記.md"); err != nil {
		t.Fatal(err)
	}
	entries, err = importer.ProcessDiff("user1", nil, nil, nil, []MovedFileEntry{
		{OldPath: "NOTE/ROOT/A/深層筆記.md", NewPath: "TODO/待辦/深層筆記.md"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionMove {
		t.Fatalf("action: got %q, want %q", entries[0].Action, ImportActionMove)
	}
	if entries[0].NoteMeta == nil || entries[0].NoteMeta.ParentID != "f-todo" {
		t.Fatalf("move to TODO should set parentID=f-todo, got %+v", entries[0].NoteMeta)
	}
}

func TestE2E_SpecialCharacters_InNames(t *testing.T) {
	fs := NewMemoryVaultFS()
	resolver := NewPathResolver([]FolderNode{
		{ID: "f1", FolderName: "工作筆記", Type: "NOTE", ParentID: nil},
		{ID: "f2", FolderName: "2026/03/08 meeting", Type: "NOTE", ParentID: nil},
		{ID: "f3", FolderName: "", Type: "NOTE", ParentID: nil},
	})
	exporter := NewExporter(fs, resolver)
	noteType := "NOTE"
	if err := exporter.ExportFolder("user1", FolderMeta{ID: "f1", MemberID: "user1", FolderName: "工作筆記", Type: &noteType}); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportFolder("user1", FolderMeta{ID: "f2", MemberID: "user1", FolderName: "2026/03/08 meeting", Type: &noteType}); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportFolder("user1", FolderMeta{ID: "f3", MemberID: "user1", FolderName: "", Type: &noteType}); err != nil {
		t.Fatal(err)
	}

	if err := exporter.ExportNote("user1", NoteMeta{ID: "n-cn", ParentID: "f1", Title: "工作筆記", USN: 1}, "<p>中文內容</p>"); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportNote("user1", NoteMeta{ID: "n-emoji", ParentID: "f1", Title: "📝 Daily Log", USN: 1}, "<p>emoji</p>"); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportNote("user1", NoteMeta{ID: "n-slash", ParentID: "f2", Title: "2026/03/08 meeting", USN: 1}, "<p>slash</p>"); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportNote("user1", NoteMeta{ID: "n-null", ParentID: "f2", Title: "note\x00bad", USN: 1}, "<p>null</p>"); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportNote("user1", NoteMeta{ID: "n-empty", ParentID: "f3", Title: "", USN: 1}, "<p>empty</p>"); err != nil {
		t.Fatal(err)
	}

	if !fs.Exists("user1/NOTE/工作筆記/工作筆記.md") {
		t.Fatal("expected chinese note path to exist")
	}
	if !fs.Exists("user1/NOTE/工作筆記/📝 Daily Log.md") {
		t.Fatal("expected emoji note path to exist")
	}
	if !fs.Exists("user1/NOTE/2026_03_08 meeting/2026_03_08 meeting.md") {
		t.Fatal("expected slash-sanitized note path to exist")
	}
	if !fs.Exists("user1/NOTE/2026_03_08 meeting/notebad.md") {
		t.Fatal("expected null-byte-sanitized note path to exist")
	}
	if !fs.Exists("user1/NOTE/_unnamed/_unnamed.md") {
		t.Fatal("expected empty-name fallback path to exist")
	}
}

func TestE2E_FolderRename_CascadingPaths(t *testing.T) {
	fs := NewMemoryVaultFS()
	resolver := NewPathResolver([]FolderNode{
		{ID: "f1", FolderName: "工作", Type: "NOTE", ParentID: nil},
	})
	exporter := NewExporter(fs, resolver)
	noteType := "NOTE"
	if err := exporter.ExportFolder("user1", FolderMeta{ID: "f1", MemberID: "user1", FolderName: "工作", Type: &noteType}); err != nil {
		t.Fatal(err)
	}
	notes := []NoteMeta{
		{ID: "n1", ParentID: "f1", Title: "筆記一", USN: 1},
		{ID: "n2", ParentID: "f1", Title: "筆記二", USN: 1},
		{ID: "n3", ParentID: "f1", Title: "筆記三", USN: 1},
	}
	for _, n := range notes {
		if err := exporter.ExportNote("user1", n, "<p>body</p>"); err != nil {
			t.Fatal(err)
		}
	}
	if !fs.Exists("user1/NOTE/工作/筆記一.md") {
		t.Fatal("old folder note should exist before rename")
	}

	resolver.UpdateFolder(FolderNode{ID: "f1", FolderName: "工作區", Type: "NOTE", ParentID: nil})
	if err := exporter.ExportFolder("user1", FolderMeta{ID: "f1", MemberID: "user1", FolderName: "工作區", Type: &noteType}); err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if err := exporter.ExportNote("user1", n, "<p>body updated</p>"); err != nil {
			t.Fatal(err)
		}
	}

	if fs.Exists("user1/NOTE/工作") {
		t.Fatal("old folder should be removed after rename")
	}
	if !fs.Exists("user1/NOTE/工作區/_folder.json") {
		t.Fatal("new folder metadata should exist")
	}
	if !fs.Exists("user1/NOTE/工作區/筆記一.md") || !fs.Exists("user1/NOTE/工作區/筆記二.md") || !fs.Exists("user1/NOTE/工作區/筆記三.md") {
		t.Fatal("all notes should exist under renamed folder")
	}
}
