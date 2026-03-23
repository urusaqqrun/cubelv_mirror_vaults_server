package executor

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
	"github.com/urusaqqrun/vault-mirror-service/model"
)

type mockWriter struct {
	mu sync.Mutex

	upsertFolderDocs []Doc
	upsertNoteDocs   []Doc
	upsertCardDocs   []Doc
	upsertChartDocs  []Doc
	upsertItemDocs   []Doc

	deleteItemIDs []string
	deleteDocs    []struct {
		collection string
		docID      string
	}
}

func (m *mockWriter) UpsertFolder(_ context.Context, _ string, doc Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertFolderDocs = append(m.upsertFolderDocs, cloneDoc(doc))
	return nil
}

func (m *mockWriter) UpsertNote(_ context.Context, _ string, doc Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertNoteDocs = append(m.upsertNoteDocs, cloneDoc(doc))
	return nil
}

func (m *mockWriter) UpsertCard(_ context.Context, _ string, doc Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertCardDocs = append(m.upsertCardDocs, cloneDoc(doc))
	return nil
}

func (m *mockWriter) UpsertChart(_ context.Context, _ string, doc Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertChartDocs = append(m.upsertChartDocs, cloneDoc(doc))
	return nil
}

func (m *mockWriter) UpsertItem(_ context.Context, _ string, doc Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertItemDocs = append(m.upsertItemDocs, cloneDoc(doc))
	return nil
}

func (m *mockWriter) DeleteItemDoc(_ context.Context, _ string, docID string, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteItemIDs = append(m.deleteItemIDs, docID)
	return nil
}

func (m *mockWriter) DeleteDocument(_ context.Context, _ string, collection, docID string, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteDocs = append(m.deleteDocs, struct {
		collection string
		docID      string
	}{collection: collection, docID: docID})
	return nil
}

type mockUSNReader struct {
	byDocID map[string]int
}

func (m *mockUSNReader) GetDocUSN(_ context.Context, _ string, _ string, docID string) (int, error) {
	if usn, ok := m.byDocID[docID]; ok {
		return usn, nil
	}
	return 0, nil
}

type mockUSNIncrementer struct {
	mu    sync.Mutex
	next  int
	calls int
}

func (m *mockUSNIncrementer) IncrementUSN(_ context.Context, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	m.calls++
	return m.next, nil
}

func cloneDoc(doc Doc) Doc {
	raw, _ := json.Marshal(doc)
	var cloned Doc
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func TestE2E_FullRoundTrip_ExportDiffImportWriteback(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	resolver := mirror.NewPathResolver([]mirror.TreeNode{
		{ID: "f-note", Name: "工作", ItemType: "NOTE_FOLDER"},
	})
	exporter := mirror.NewExporter(fs, resolver)

	if _, err := exporter.ExportItem("user1", mirrorItem("f-note", "工作", "NOTE_FOLDER", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.ExportItem("user1", mirrorItem("n1", "會議A", "NOTE", "f-note")); err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.ExportItem("user1", mirrorItem("n2", "會議B", "NOTE", "f-note")); err != nil {
		t.Fatal(err)
	}

	beforeSnap, err := TakeSnapshot(fs, "user1")
	if err != nil {
		t.Fatal(err)
	}

	n1Updated := mirrorItem("n1", "會議A", "NOTE", "f-note")
	n1Updated.Fields["content"] = "<p>A modified</p>"
	if _, err := exporter.ExportItem("user1", n1Updated); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile("user1/NOTE/工作/ai-new.json", []byte(`{"id":"","name":"AI created","itemType":"NOTE","fields":{"parentID":"f-note","content":"draft"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("user1/NOTE/工作/會議B.json"); err != nil {
		t.Fatal(err)
	}

	afterSnap, err := TakeSnapshot(fs, "user1")
	if err != nil {
		t.Fatal(err)
	}
	diff := ComputeDiff(beforeSnap, afterSnap)

	importer := mirror.NewImporter(fs)
	entries, err := importer.ProcessDiff(
		"user1",
		diff.Created,
		diff.Modified,
		diff.Deleted,
		nil,
		map[string]string{"NOTE/工作/會議B.json": "n2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	writer := &mockWriter{}
	result := WriteBack(context.Background(), writer, nil, nil, "user1", entries, 0)
	if result.Errors != 0 {
		t.Fatalf("errors: got %d, want 0", result.Errors)
	}
	if result.Created != 1 || result.Updated != 1 || result.Deleted != 1 {
		t.Fatalf("unexpected writeback result: %+v", result)
	}
	if !slices.Contains(writer.deleteItemIDs, "n2") {
		t.Fatalf("deleted IDs should contain n2, got %v", writer.deleteItemIDs)
	}
	if len(writer.upsertItemDocs) != 2 {
		t.Fatalf("upsert item docs: got %d, want 2", len(writer.upsertItemDocs))
	}
}

func TestE2E_ConcurrentConflict_SkipsNewerDBDoc(t *testing.T) {
	entries := []mirror.ImportEntry{
		{
			Action:     mirror.ImportActionUpdate,
			Collection: "item",
			ItemData: &mirror.ItemMirrorData{
				ID:       "n1",
				Name:     "會議A",
				ItemType: "NOTE",
				Fields: map[string]interface{}{
					"parentID": "f1",
				},
			},
		},
	}
	writer := &mockWriter{}
	reader := &mockUSNReader{byDocID: map[string]int{"n1": 9}}

	result := WriteBack(context.Background(), writer, reader, nil, "user1", entries, 5)
	if result.Skipped != 1 {
		t.Fatalf("skipped: got %d, want 1", result.Skipped)
	}
	if len(writer.upsertItemDocs) != 0 {
		t.Fatal("conflicted doc should not be written back")
	}
}

func mirrorItem(id, name, itemType, parentID string) *model.Item {
	fields := map[string]interface{}{}
	if parentID != "" {
		fields["parentID"] = parentID
	}
	return &model.Item{
		ID:     id,
		Name:   name,
		Type:   itemType,
		Fields: fields,
	}
}
