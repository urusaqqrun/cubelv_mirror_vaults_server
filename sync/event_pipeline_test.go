package sync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
	"github.com/urusaqqrun/vault-mirror-service/model"
)

type mockDataReader struct {
	folders map[string]*model.Folder
	notes   map[string]*model.Note
	cards   map[string]*model.Card
	charts  map[string]*model.Chart
	items   map[string]*model.Item
}

func (m *mockDataReader) ListFolders(_ context.Context, _ string) ([]*model.Folder, error) {
	out := make([]*model.Folder, 0, len(m.folders))
	for _, f := range m.folders {
		out = append(out, f)
	}
	return out, nil
}

func (m *mockDataReader) GetFolder(_ context.Context, _ string, id string) (*model.Folder, error) {
	return m.folders[id], nil
}

func (m *mockDataReader) GetNote(_ context.Context, _ string, id string) (*model.Note, error) {
	return m.notes[id], nil
}

func (m *mockDataReader) GetCard(_ context.Context, _ string, id string) (*model.Card, error) {
	return m.cards[id], nil
}

func (m *mockDataReader) GetChart(_ context.Context, _ string, id string) (*model.Chart, error) {
	return m.charts[id], nil
}

func (m *mockDataReader) GetItem(_ context.Context, _ string, id string) (*model.Item, error) {
	return m.items[id], nil
}

func (m *mockDataReader) ListAllItems(_ context.Context, _ string) ([]*model.Item, error) {
	out := make([]*model.Item, 0, len(m.items))
	for _, item := range m.items {
		out = append(out, item)
	}
	return out, nil
}

func ptr(s string) *string { return &s }

func TestEventPipeline_ItemCreate_ExportsNestedJSON(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	reader := &mockDataReader{
		items: map[string]*model.Item{
			"f1": {ID: "f1", Name: "工作", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
			"n1": {ID: "n1", Name: "筆記A", Type: "NOTE", Fields: map[string]interface{}{"parentID": "f1", "content": "hello"}},
			"n2": {ID: "n2", Name: "A評論", Type: "NOTE", Fields: map[string]interface{}{"parentID": "n1", "content": "reply"}},
		},
	}

	h := NewSyncEventHandler(fs, reader)
	for _, docID := range []string{"f1", "n1", "n2"} {
		if err := h.HandleEvent(context.Background(), SyncEvent{Collection: "item", UserID: "u1", DocID: docID, Action: "create"}); err != nil {
			t.Fatal(err)
		}
	}

	if !fs.Exists("u1/NOTE/工作.json") {
		t.Fatal("expected folder json to be exported")
	}
	if !fs.Exists("u1/NOTE/工作/筆記A.json") {
		t.Fatal("expected parent note json to be exported")
	}
	if !fs.Exists("u1/NOTE/工作/筆記A/A評論.json") {
		t.Fatal("expected child note json to be exported")
	}
}

func TestEventPipeline_FolderUpdate_ExportsSiblingJSON(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	noteType := "NOTE"
	reader := &mockDataReader{
		folders: map[string]*model.Folder{
			"f1": {ID: "f1", FolderName: "工作", Type: &noteType, Usn: 2},
		},
		items: map[string]*model.Item{
			"f1": {ID: "f1", Name: "工作", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
		},
	}

	h := NewSyncEventHandler(fs, reader)
	if err := h.HandleEvent(context.Background(), SyncEvent{Collection: "folder", UserID: "u1", DocID: "f1", Action: "update"}); err != nil {
		t.Fatal(err)
	}
	if !fs.Exists("u1/NOTE/工作.json") {
		t.Fatal("expected folder json to be exported")
	}
}

func TestEventPipeline_NoteCreate_ExportsJSON(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	title := "新筆記"
	content := "<p>Hello</p>"
	reader := &mockDataReader{
		notes: map[string]*model.Note{
			"n1": {ID: "n1", Title: &title, Content: &content, ParentID: "f1", Usn: 3, CreateAt: 1, UpdateAt: 2},
		},
		items: map[string]*model.Item{
			"f1": {ID: "f1", Name: "工作", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
		},
	}

	h := NewSyncEventHandler(fs, reader)
	if err := h.HandleEvent(context.Background(), SyncEvent{Collection: "note", UserID: "u1", DocID: "n1", Action: "create"}); err != nil {
		t.Fatal(err)
	}
	if !fs.Exists("u1/NOTE/工作/新筆記.json") {
		t.Fatal("expected note json to be exported")
	}
}

func TestEventPipeline_ItemDelete_RemovesJSONAndContainer(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	_ = fs.WriteFile("u1/NOTE/工作/test.json", []byte(`{"id":"n1","name":"test","itemType":"NOTE","fields":{"parentID":"f1"}}`))
	_ = fs.WriteFile("u1/NOTE/工作/test/reply.json", []byte(`{"id":"n2","name":"reply","itemType":"NOTE","fields":{"parentID":"n1"}}`))

	reader := &mockDataReader{items: map[string]*model.Item{}}
	h := NewSyncEventHandler(fs, reader)
	if err := h.HandleEvent(context.Background(), SyncEvent{Collection: "item", UserID: "u1", DocID: "n1", Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	if fs.Exists("u1/NOTE/工作/test.json") || fs.Exists("u1/NOTE/工作/test") {
		t.Fatal("expected deleted item projection to be removed")
	}
}

type testVaultLocker struct {
	locked map[string]bool
}

func (l *testVaultLocker) IsLocked(userId string) bool {
	return l.locked[userId]
}

func TestHandleEvent_VaultLocked_ReturnsError(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	reader := &mockDataReader{items: map[string]*model.Item{}}
	h := NewSyncEventHandler(fs, reader)

	locker := &testVaultLocker{locked: map[string]bool{"u1": true}}
	h.SetLocker(locker)

	err := h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u1", DocID: "n1", Action: "create",
	})
	if !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked, got %v", err)
	}

	err = h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u2", DocID: "n2", Action: "create",
	})
	if errors.Is(err, ErrVaultLocked) {
		t.Fatal("u2 不該被鎖定")
	}
}

func TestHandleEvent_VaultUnlocked_ProcessesNormally(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	reader := &mockDataReader{
		items: map[string]*model.Item{
			"f1": {ID: "f1", Name: "Work", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
			"n1": {ID: "n1", Name: "test", Type: "NOTE", Fields: map[string]interface{}{"parentID": "f1", "content": "<p>ok</p>"}},
		},
	}
	h := NewSyncEventHandler(fs, reader)

	locker := &testVaultLocker{locked: map[string]bool{"u1": true}}
	h.SetLocker(locker)

	err := h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u1", DocID: "n1", Action: "create",
	})
	if !errors.Is(err, ErrVaultLocked) {
		t.Fatal("should be locked")
	}

	locker.locked["u1"] = false
	err = h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u1", DocID: "n1", Action: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fs.Exists("u1/NOTE/Work/test.json") {
		t.Fatal("item should be exported after unlock")
	}
}

type countingDataReader struct {
	mockDataReader
	listAllItemsCalls int32
}

func (m *countingDataReader) ListAllItems(ctx context.Context, userID string) ([]*model.Item, error) {
	atomic.AddInt32(&m.listAllItemsCalls, 1)
	return m.mockDataReader.ListAllItems(ctx, userID)
}

func TestResolverCache_ReducesListAllItemsCalls(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	reader := &countingDataReader{
		mockDataReader: mockDataReader{
			items: map[string]*model.Item{
				"f1": {ID: "f1", Name: "Work", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
				"n1": {ID: "n1", Name: "note1", Type: "NOTE", Fields: map[string]interface{}{"parentID": "f1", "content": "<p>x</p>"}},
				"n2": {ID: "n2", Name: "note2", Type: "NOTE", Fields: map[string]interface{}{"parentID": "f1", "content": "<p>x</p>"}},
			},
		},
	}

	h := NewSyncEventHandler(fs, reader)
	for i := 0; i < 5; i++ {
		docID := "n1"
		if i%2 == 1 {
			docID = "n2"
		}
		h.HandleEvent(context.Background(), SyncEvent{
			Collection: "item", UserID: "u1", DocID: docID, Action: "update",
		})
	}

	calls := atomic.LoadInt32(&reader.listAllItemsCalls)
	if calls != 5 {
		t.Errorf("expected ListAllItems called once per item event after full invalidation, got %d", calls)
	}
}

func TestResolverCache_InvalidatedOnFolderEvent(t *testing.T) {
	fs := mirror.NewMemoryVaultFS()
	reader := &countingDataReader{
		mockDataReader: mockDataReader{
			folders: map[string]*model.Folder{
				"f1": {ID: "f1", FolderName: "Work", Type: ptr("NOTE")},
			},
			items: map[string]*model.Item{
				"f1": {ID: "f1", Name: "Work", Type: "NOTE_FOLDER", Fields: map[string]interface{}{}},
				"n1": {ID: "n1", Name: "n", Type: "NOTE", Fields: map[string]interface{}{"parentID": "f1", "content": "<p>x</p>"}},
			},
		},
	}

	h := NewSyncEventHandler(fs, reader)
	h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u1", DocID: "n1", Action: "update",
	})
	h.HandleEvent(context.Background(), SyncEvent{
		Collection: "folder", UserID: "u1", DocID: "f1", Action: "update",
	})
	h.HandleEvent(context.Background(), SyncEvent{
		Collection: "item", UserID: "u1", DocID: "n1", Action: "update",
	})

	calls := atomic.LoadInt32(&reader.listAllItemsCalls)
	if calls != 3 {
		t.Errorf("expected ListAllItems == 3 with item invalidation plus folder invalidation, got %d", calls)
	}
}
