package mirror

import (
	"testing"
)

func setupImporterFS() (*Importer, *MemoryVaultFS) {
	fs := NewMemoryVaultFS()
	return NewImporter(fs), fs
}

func TestProcessDiff_CreatedNote(t *testing.T) {
	imp, fs := setupImporterFS()

	noteJSON := `{"id":"n1","name":"新筆記","itemType":"NOTE","fields":{"parentID":"f1","content":"# 新筆記\n\n這是新建的筆記內容","tags":["工作"]}}`
	fs.WriteFile("user1/NOTE/工作/新筆記.json", []byte(noteJSON))

	entries, err := imp.ProcessDiff("user1", []string{"NOTE/工作/新筆記.json"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionCreate {
		t.Errorf("action: got %q, want %q", entries[0].Action, ImportActionCreate)
	}
	if entries[0].Collection != "item" {
		t.Errorf("collection: got %q, want %q", entries[0].Collection, "item")
	}
	if entries[0].ItemType != "NOTE" {
		t.Errorf("itemType: got %q, want %q", entries[0].ItemType, "NOTE")
	}
	if entries[0].ItemData == nil {
		t.Fatal("ItemData should not be nil")
	}
	if entries[0].ItemData.ID != "n1" {
		t.Errorf("note ID: got %q, want %q", entries[0].ItemData.ID, "n1")
	}
}

func TestProcessDiff_ModifiedNote(t *testing.T) {
	imp, fs := setupImporterFS()

	noteJSON := `{"id":"n1","name":"修改的筆記","itemType":"NOTE","fields":{"parentID":"f1","content":"更新後的內容"}}`
	fs.WriteFile("user1/NOTE/工作/修改的筆記.json", []byte(noteJSON))

	entries, err := imp.ProcessDiff("user1", nil, []string{"NOTE/工作/修改的筆記.json"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionUpdate {
		t.Errorf("action: got %q, want %q", entries[0].Action, ImportActionUpdate)
	}
}

func TestProcessDiff_DeletedNote(t *testing.T) {
	imp, _ := setupImporterFS()

	entries, err := imp.ProcessDiff("user1", nil, nil, []string{"NOTE/工作/刪除的筆記.json"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Action != ImportActionDelete {
		t.Errorf("action: got %q, want %q", entries[0].Action, ImportActionDelete)
	}
}

func TestProcessDiff_DeletedNote_WithBeforeIDMap(t *testing.T) {
	imp, _ := setupImporterFS()

	beforeIDMap := map[string]string{"NOTE/工作/刪除.json": "del-id"}
	entries, err := imp.ProcessDiff("user1", nil, nil, []string{"NOTE/工作/刪除.json"}, nil, beforeIDMap)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].DocID != "del-id" {
		t.Errorf("docID: got %q, want %q", entries[0].DocID, "del-id")
	}
}

func TestProcessDiff_MovedNote(t *testing.T) {
	imp, fs := setupImporterFS()

	noteJSON := `{"id":"n1","name":"搬移的筆記","itemType":"NOTE","fields":{"parentID":"f2"}}`
	fs.WriteFile("user1/NOTE/生活/搬移的筆記.json", []byte(noteJSON))

	entries, err := imp.ProcessDiff("user1", nil, nil, nil, []MovedFileEntry{
		{OldPath: "NOTE/工作/搬移的筆記.json", NewPath: "NOTE/生活/搬移的筆記.json"},
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
	if entries[0].OldPath != "NOTE/工作/搬移的筆記.json" {
		t.Errorf("oldPath: got %q", entries[0].OldPath)
	}
}

func TestProcessDiff_FolderCreated(t *testing.T) {
	imp, fs := setupImporterFS()

	folderJSON := `{"id":"f-new","name":"新目錄","itemType":"NOTE_FOLDER","fields":{"noteNum":0}}`
	fs.WriteFile("user1/NOTE/新目錄.json", []byte(folderJSON))

	entries, err := imp.ProcessDiff("user1", []string{"NOTE/新目錄.json"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Collection != "item" {
		t.Errorf("collection: got %q, want %q", entries[0].Collection, "item")
	}
	if entries[0].ItemType != "NOTE_FOLDER" {
		t.Errorf("itemType: got %q, want %q", entries[0].ItemType, "NOTE_FOLDER")
	}
	if entries[0].ItemData == nil {
		t.Fatal("ItemData should not be nil")
	}
	if entries[0].ItemData.ID != "f-new" {
		t.Errorf("folder ID: got %q, want %q", entries[0].ItemData.ID, "f-new")
	}
}

func TestProcessDiff_CardCreated(t *testing.T) {
	imp, fs := setupImporterFS()

	cardJSON := `{"id":"card1","name":"鼎泰豐","itemType":"CARD","fields":{"parentID":"c1","fields":"{\"店名\":\"鼎泰豐\"}"}}`
	fs.WriteFile("user1/CARD/美食/鼎泰豐.json", []byte(cardJSON))

	entries, err := imp.ProcessDiff("user1", []string{"CARD/美食/鼎泰豐.json"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Collection != "item" {
		t.Errorf("collection: got %q, want %q", entries[0].Collection, "item")
	}
	if entries[0].ItemType != "CARD" {
		t.Errorf("itemType: got %q, want %q", entries[0].ItemType, "CARD")
	}
	if entries[0].ItemData.Name != "鼎泰豐" {
		t.Errorf("card name: got %q", entries[0].ItemData.Name)
	}
}

func TestProcessDiff_MixedChanges(t *testing.T) {
	imp, fs := setupImporterFS()

	fs.WriteFile("user1/NOTE/工作/新.json", []byte(`{"id":"n1","name":"新","itemType":"NOTE","fields":{"parentID":"f1","content":"content"}}`))
	fs.WriteFile("user1/NOTE/工作/改.json", []byte(`{"id":"n2","name":"改","itemType":"NOTE","fields":{"parentID":"f1","content":"content"}}`))

	entries, err := imp.ProcessDiff("user1",
		[]string{"NOTE/工作/新.json"},
		[]string{"NOTE/工作/改.json"},
		[]string{"NOTE/工作/刪.json"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}

// --- 新格式 JSON 解析 ---

func TestProcessDiff_NewFormatJSON_Created(t *testing.T) {
	imp, fs := setupImporterFS()

	itemJSON := `{"id":"item1","name":"看板1","itemType":"KANBAN","fields":{"color":"red"}}`
	fs.WriteFile("user1/KANBAN/看板/看板1.json", []byte(itemJSON))

	entries, err := imp.ProcessDiff("user1", []string{"KANBAN/看板/看板1.json"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.ItemData == nil {
		t.Fatal("ItemData should not be nil for new format")
	}
	if e.ItemData.ID != "item1" {
		t.Errorf("ID: got %q, want %q", e.ItemData.ID, "item1")
	}
	if e.ItemData.ItemType != "KANBAN" {
		t.Errorf("ItemType: got %q, want %q", e.ItemData.ItemType, "KANBAN")
	}
	if e.ItemType != "KANBAN" {
		t.Errorf("entry.ItemType: got %q, want %q", e.ItemType, "KANBAN")
	}
}

func TestProcessDiff_NewFormat_FallbackNameCleared(t *testing.T) {
	imp, fs := setupImporterFS()

	itemJSON := `{"id":"abc123","name":"untitled_abc123","itemType":"NOTE","fields":{}}`
	fs.WriteFile("user1/NOTE/工作/untitled_abc123.json", []byte(itemJSON))

	entries, err := imp.ProcessDiff("user1", []string{"NOTE/工作/untitled_abc123.json"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].ItemData.Name != "" {
		t.Errorf("fallback name should be cleared, got %q", entries[0].ItemData.Name)
	}
}

func TestProcessDiff_NonJSONFile_Skipped(t *testing.T) {
	imp, fs := setupImporterFS()

	fs.WriteFile("user1/NOTE/工作/test.md", []byte("# Hello"))

	entries, err := imp.ProcessDiff("user1", []string{"NOTE/工作/test.md"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Non-JSON files are skipped (logged), so entries should be empty
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0 (non-json should be skipped)", len(entries))
	}
}
