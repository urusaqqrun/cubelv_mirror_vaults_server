package mirror

import (
	"encoding/json"

	"github.com/urusaqqrun/vault-mirror-service/model"
)

// ItemToNoteMeta 將 Item（NOTE/TODO）轉換為 NoteMeta + HTML content
func ItemToNoteMeta(item *model.Item) (NoteMeta, string) {
	f := item.Fields
	meta := NoteMeta{
		ID:        item.ID,
		ParentID:  model.StrPtrDeref(model.StrPtrField(f, "parentID")),
		FolderID:  strFieldDefault(f, "folderID", ""),
		Title:     item.GetTitle(),
		USN:       item.GetUSN(),
		Tags:      model.StringSliceField(f, "tags"),
		IsNew:     model.BoolField(f, "isNew"),
		CreatedAt: model.Int64StrField(f, "createdAt"),
		UpdatedAt: model.Int64StrField(f, "updatedAt"),
	}
	if meta.UpdatedAt == "" {
		meta.UpdatedAt = model.Int64StrField(f, "updateAt")
	}
	if v := model.StrPtrField(f, "orderAt"); v != nil {
		meta.OrderAt = *v
	}
	if v := model.StrPtrField(f, "status"); v != nil {
		meta.Status = *v
	}
	if v := model.StrPtrField(f, "aiTitle"); v != nil {
		meta.AiTitle = *v
	}
	meta.AiTags = model.StringSliceField(f, "aiTags")
	meta.ImgURLs = model.StringSliceField(f, "imgURLs")

	content := strFieldDefault(f, "content", "")
	return meta, content
}

// ItemToFolderMeta 將 Item（FOLDER）轉換為 FolderMeta
func ItemToFolderMeta(item *model.Item) FolderMeta {
	f := item.Fields
	meta := FolderMeta{
		ID:         item.ID,
		MemberID:   item.GetMemberID(),
		FolderName: item.GetName(),
		Type:       model.StrPtrField(f, "folderType"),
		ParentID:   model.StrPtrField(f, "parentID"),
		OrderAt:    model.StrPtrField(f, "orderAt"),
		Icon:       model.StrPtrField(f, "icon"),
		CreatedAt:  model.Int64StrField(f, "createdAt"),
		UpdatedAt:  model.Int64StrField(f, "updatedAt"),
		USN:        item.GetUSN(),
		NoteNum:    model.Int64Field(f, "noteNum"),
		IsTemp:     model.BoolField(f, "isTemp"),

		FolderSummary:     model.StrPtrField(f, "folderSummary"),
		AiFolderName:      model.StrPtrField(f, "aiFolderName"),
		AiFolderSummary:   model.StrPtrField(f, "aiFolderSummary"),
		AiInstruction:     model.StrPtrField(f, "aiInstruction"),
		AutoUpdateSummary: model.BoolField(f, "autoUpdateSummary"),

		TemplateHTML:    model.StrPtrField(f, "templateHtml"),
		TemplateCSS:     model.StrPtrField(f, "templateCss"),
		UIPrompt:        model.StrPtrField(f, "uiPrompt"),
		IsShared:        model.BoolField(f, "isShared"),
		Searchable:      model.BoolField(f, "searchable"),
		AllowContribute: model.BoolField(f, "allowContribute"),
		ChartKind:       model.StrPtrField(f, "chartKind"),
	}
	decodeField(f, "indexes", &meta.Indexes)
	decodeField(f, "isSummarizedNoteIds", &meta.IsSummarizedNoteIds)
	decodeField(f, "fields", &meta.Fields)
	decodeField(f, "templateHistory", &meta.TemplateHistory)
	decodeField(f, "sharers", &meta.Sharers)
	return meta
}

// ItemToCardMeta 將 Item（CARD）轉換為 CardMeta
func ItemToCardMeta(item *model.Item) CardMeta {
	f := item.Fields
	return CardMeta{
		ID:            item.ID,
		MemberID:      item.GetMemberID(),
		ContributorID: model.StrPtrField(f, "contributorId"),
		ParentID:      item.GetParentID(),
		Name:          item.GetName(),
		Fields:        model.StrPtrField(f, "fields"),
		Reviews:       model.StrPtrField(f, "reviews"),
		Coordinates:   model.StrPtrField(f, "coordinates"),
		OrderAt:       model.StrPtrField(f, "orderAt"),
		IsDeleted:     model.BoolField(f, "isDeleted"),
		CreatedAt:     model.Int64StrField(f, "createdAt"),
		UpdatedAt:     model.Int64StrField(f, "updatedAt"),
		USN:           item.GetUSN(),
	}
}

// ItemToChartMeta 將 Item（CHART）轉換為 CardMeta（Chart 共用 CardMeta 結構）
func ItemToChartMeta(item *model.Item) CardMeta {
	f := item.Fields
	return CardMeta{
		ID:        item.ID,
		MemberID:  item.GetMemberID(),
		ParentID:  item.GetParentID(),
		Name:      item.GetName(),
		Fields:    model.StrPtrField(f, "data"),
		OrderAt:   model.StrPtrField(f, "orderAt"),
		IsDeleted: model.BoolField(f, "isDeleted"),
		CreatedAt: model.Int64StrField(f, "createdAt"),
		UpdatedAt: model.Int64StrField(f, "updatedAt"),
		USN:       item.GetUSN(),
	}
}

// ItemFolderType 回傳 FOLDER item 的子類型（NOTE/CARD/CHART），用於 PathResolver
func ItemFolderType(item *model.Item) string {
	if v := model.StrPtrField(item.Fields, "folderType"); v != nil {
		return *v
	}
	return "NOTE"
}

func strFieldDefault(fields map[string]interface{}, key, def string) string {
	if v, ok := fields[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// decodeField 將 map 的動態值解碼為指定型別（缺值/型別不符時保持原值）
func decodeField(fields map[string]interface{}, key string, out interface{}) {
	v, ok := fields[key]
	if !ok || v == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, out)
}
