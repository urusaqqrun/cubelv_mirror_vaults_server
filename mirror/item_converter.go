package mirror

import (
	"github.com/urusaqqrun/vault-mirror-service/model"
)

// ItemToMirrorData 將 model.Item 轉換為 ItemMirrorData（新 JSON 鏡像格式）
func ItemToMirrorData(item *model.Item) ItemMirrorData {
	name := item.GetName()
	if name == "" {
		name = VaultFallbackName(item.ID)
	}
	// 深拷貝 Fields 避免共用 map reference 導致原始 Item 被異動
	fields := make(map[string]interface{}, len(item.Fields))
	for k, v := range item.Fields {
		fields[k] = v
	}
	return ItemMirrorData{
		ID:       item.ID,
		Name:     name,
		ItemType: item.Type,
		Fields:   fields,
	}
}
