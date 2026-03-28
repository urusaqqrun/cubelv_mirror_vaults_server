package mirror

import (
	"testing"
)

// --- ItemMirrorData 序列化 / 反序列化 ---

func TestVaultFallbackName(t *testing.T) {
	got := VaultFallbackName("69a722717998c644cb2610a0")
	if got != "untitled_69a722717998c644cb2610a0" {
		t.Errorf("got %q, want %q", got, "untitled_69a722717998c644cb2610a0")
	}
}

func TestIsVaultFallbackName(t *testing.T) {
	id := "abc123"
	if !IsVaultFallbackName("untitled_abc123", id) {
		t.Error("should match fallback name")
	}
	if IsVaultFallbackName("my-note", id) {
		t.Error("should not match non-fallback name")
	}
	if IsVaultFallbackName("untitled_xyz", id) {
		t.Error("should not match fallback for different id")
	}
}

func TestItemToMirrorJSON_Roundtrip(t *testing.T) {
	data := ItemMirrorData{
		ID:       "item1",
		Name:     "測試項目",
		ItemType: "KANBAN",
		Fields:   map[string]interface{}{"color": "red", "count": float64(42)},
	}
	jsonBytes, err := ItemToMirrorJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := MirrorJSONToItem(jsonBytes)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != data.ID || parsed.Name != data.Name || parsed.ItemType != data.ItemType {
		t.Errorf("roundtrip mismatch: got %+v", parsed)
	}
	if parsed.Fields["color"] != "red" {
		t.Errorf("fields.color: got %v", parsed.Fields["color"])
	}
}

func TestMirrorJSONToItem_EmptyID_Allowed(t *testing.T) {
	item, err := MirrorJSONToItem([]byte(`{"name":"test","itemType":"NOTE","fields":{}}`))
	if err != nil {
		t.Fatalf("empty id should be allowed for AI-created items, got error: %v", err)
	}
	if item.Name != "test" || item.ItemType != "NOTE" {
		t.Errorf("unexpected result: %+v", item)
	}
}

func TestMirrorJSONToItem_MissingItemType(t *testing.T) {
	_, err := MirrorJSONToItem([]byte(`{"id":"x","name":"test","fields":{}}`))
	if err == nil {
		t.Error("should error when itemType is missing")
	}
}

func TestMirrorJSONToItem_InvalidJSON(t *testing.T) {
	_, err := MirrorJSONToItem([]byte(`not valid json`))
	if err == nil {
		t.Error("should error on invalid json")
	}
}

func TestItemToJSON_Basic(t *testing.T) {
	doc := map[string]interface{}{
		"id":       "item1",
		"name":     "test",
		"itemType": "NOTE",
	}
	data, err := ItemToJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("should produce non-empty JSON")
	}
}

func TestJSONToItem_Basic(t *testing.T) {
	raw := []byte(`{"id":"item1","name":"test","itemType":"NOTE"}`)
	doc, err := JSONToItem(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc["id"] != "item1" {
		t.Errorf("id: got %v", doc["id"])
	}
}
