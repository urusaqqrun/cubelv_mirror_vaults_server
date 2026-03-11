package executor

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/urusaqqrun/vault-mirror-service/mirror"
)

func TestFolderMetaToItemBson_PreservesFolderItemType(t *testing.T) {
	noteType := "NOTE"
	doc := folderMetaToItemBson(&mirror.FolderMeta{
		ID:         "f1",
		MemberID:   "u1",
		FolderName: "資料夾",
		Type:       &noteType,
	}, "NOTE_FOLDER", 10)

	if got, _ := doc["itemType"].(string); got != "NOTE_FOLDER" {
		t.Fatalf("itemType mismatch: got %q, want %q", got, "NOTE_FOLDER")
	}
}

func TestFolderMetaToItemBson_InvalidTypeFallsBack(t *testing.T) {
	doc := folderMetaToItemBson(&mirror.FolderMeta{
		ID:         "f1",
		MemberID:   "u1",
		FolderName: "資料夾",
	}, "UNKNOWN", 0)

	if got, _ := doc["itemType"].(string); got != "FOLDER" {
		t.Fatalf("itemType fallback mismatch: got %q, want %q", got, "FOLDER")
	}
}

func TestChartAndCardLegacyBson_IncludeIsDeleted(t *testing.T) {
	deleted := true
	cardDoc := cardMetaToBson(&mirror.CardMeta{
		ID:        "c1",
		ParentID:  "p1",
		Name:      "card",
		USN:       1,
		IsDeleted: deleted,
	})
	chartDoc := chartMetaToBson(&mirror.CardMeta{
		ID:        "h1",
		ParentID:  "p1",
		Name:      "chart",
		USN:       1,
		IsDeleted: deleted,
	})

	if got, _ := cardDoc["isDeleted"].(bool); !got {
		t.Fatal("card bson should include isDeleted=true")
	}
	if got, _ := chartDoc["isDeleted"].(bool); !got {
		t.Fatal("chart bson should include isDeleted=true")
	}
}

func TestEnsureDocID_CreateWithEmptyID(t *testing.T) {
	doc := bson.M{"_id": "", "itemType": "NOTE"}
	ensureDocID(doc, mirror.ImportActionCreate)
	id, ok := doc["_id"].(string)
	if !ok || id == "" {
		t.Fatal("ensureDocID should generate a non-empty _id for create action")
	}
	if len(id) != 24 {
		t.Fatalf("generated _id should be 24-char hex, got %q (len=%d)", id, len(id))
	}
}

func TestEnsureDocID_CreateWithExistingID(t *testing.T) {
	doc := bson.M{"_id": "existing-id", "itemType": "NOTE"}
	ensureDocID(doc, mirror.ImportActionCreate)
	if doc["_id"] != "existing-id" {
		t.Fatalf("ensureDocID should not overwrite existing _id, got %q", doc["_id"])
	}
}

func TestEnsureDocID_UpdateWithEmptyID(t *testing.T) {
	doc := bson.M{"_id": "", "itemType": "NOTE"}
	ensureDocID(doc, mirror.ImportActionUpdate)
	if doc["_id"] != "" {
		t.Fatal("ensureDocID should not generate _id for non-create actions")
	}
}

// --- 新格式 itemDataToItemBson ---

func TestItemDataToItemBson_BasicMapping(t *testing.T) {
	data := &mirror.ItemMirrorData{
		ID:       "item1",
		Name:     "測試",
		ItemType: "KANBAN",
		Fields:   map[string]interface{}{"color": "red", "size": float64(5)},
	}
	doc := itemDataToItemBson(data, 10)

	if doc["_id"] != "item1" {
		t.Errorf("_id: got %v", doc["_id"])
	}
	if doc["name"] != "測試" {
		t.Errorf("name: got %v", doc["name"])
	}
	if doc["itemType"] != "KANBAN" {
		t.Errorf("itemType: got %v", doc["itemType"])
	}
	fields, ok := doc["fields"].(bson.M)
	if !ok {
		t.Fatal("fields should be bson.M")
	}
	if fields["color"] != "red" {
		t.Errorf("fields.color: got %v", fields["color"])
	}
	if fields["usn"] != 10 {
		t.Errorf("fields.usn: got %v", fields["usn"])
	}
	if _, ok := fields["updatedAt"]; !ok {
		t.Error("fields.updatedAt should be set")
	}
}

func TestItemDataToItemBson_ZeroUSN_NotSet(t *testing.T) {
	data := &mirror.ItemMirrorData{
		ID:       "item2",
		Name:     "no-usn",
		ItemType: "NOTE",
		Fields:   map[string]interface{}{},
	}
	doc := itemDataToItemBson(data, 0)
	fields := doc["fields"].(bson.M)
	if _, ok := fields["usn"]; ok {
		t.Error("usn should not be set when value is 0")
	}
}

func TestItemDataToItemBson_DoesNotMutateOriginal(t *testing.T) {
	originalFields := map[string]interface{}{"key": "value"}
	data := &mirror.ItemMirrorData{
		ID:       "item3",
		Name:     "test",
		ItemType: "NOTE",
		Fields:   originalFields,
	}
	doc := itemDataToItemBson(data, 5)
	fields := doc["fields"].(bson.M)
	fields["injected"] = "bad"

	if _, ok := originalFields["injected"]; ok {
		t.Fatal("itemDataToItemBson should not share Fields map with original")
	}
}
