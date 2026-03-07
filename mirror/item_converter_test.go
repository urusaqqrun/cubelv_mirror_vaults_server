package mirror

import (
	"testing"

	"github.com/urusaqqrun/vault-mirror-service/model"
)

func TestItemToNoteMeta_TitleAndUpdatedAtFallback(t *testing.T) {
	item := &model.Item{
		ID:   "n1",
		Type: model.ItemTypeNote,
		Fields: map[string]interface{}{
			"name":      "來自舊欄位標題",
			"parentID":  "f1",
			"usn":       12,
			"createdAt": int64(1700000000000),
			"updateAt":  int64(1709000000000),
		},
	}

	meta, _ := ItemToNoteMeta(item)
	if meta.Title != "來自舊欄位標題" {
		t.Fatalf("title fallback failed: got %q", meta.Title)
	}
	if meta.UpdatedAt != "1709000000000" {
		t.Fatalf("updatedAt fallback failed: got %q", meta.UpdatedAt)
	}
}

func TestItemToFolderMeta_DecodeComplexArrays(t *testing.T) {
	item := &model.Item{
		ID:   "f1",
		Type: model.ItemTypeFolder,
		Fields: map[string]interface{}{
			"memberID": "u1",
			"name":     "工作",
			"usn":      5,
			"indexes": []interface{}{
				map[string]interface{}{
					"name":       "會議",
					"notes":      []interface{}{"n1", "n2"},
					"isReserved": true,
				},
			},
			"isSummarizedNoteIds": []interface{}{"n1"},
		},
	}

	meta := ItemToFolderMeta(item)
	if len(meta.Indexes) != 1 || meta.Indexes[0].Name != "會議" {
		t.Fatalf("indexes decode failed: %+v", meta.Indexes)
	}
	if len(meta.IsSummarizedNoteIds) != 1 || meta.IsSummarizedNoteIds[0] == nil || *meta.IsSummarizedNoteIds[0] != "n1" {
		t.Fatalf("isSummarizedNoteIds decode failed: %+v", meta.IsSummarizedNoteIds)
	}
}
