package model

// Folder 統一容器（NOTE / TODO / CARD / CHART）
type Folder struct {
	ID         string  `bson:"_id"`
	FolderName string  `bson:"folderName"`
	Type       *string `bson:"type,omitempty"`
	ParentID   *string `json:"parentID,omitempty" bson:"parentID,omitempty"`
	OrderAt    *string `bson:"orderAt,omitempty"`
	Icon       *string `bson:"icon,omitempty"`
	CreatedAt  string  `bson:"createdAt"`
	UpdatedAt  string  `bson:"updatedAt"`
	Usn        int     `bson:"usn"`
	NoteNum    int64   `bson:"noteNum"`
	IsTemp     bool    `bson:"isTemp"`

	// NOTE/TODO 專用
	Indexes           []*Index  `bson:"indexes,omitempty"`
	FolderSummary     *string   `bson:"folderSummary,omitempty"`
	AiFolderName      *string   `bson:"aiFolderName,omitempty"`
	AiFolderSummary   *string   `bson:"aiFolderSummary,omitempty"`
	AiInstruction     *string   `bson:"aiInstruction,omitempty"`
	AutoUpdateSummary bool      `bson:"autoUpdateSummary,omitempty"`
	IsSummarizedNoteIds []*string `bson:"isSummarizedNoteIds,omitempty"`

	// CARD 專用
	Fields          []*CardFieldDef         `json:"fields,omitempty" bson:"fields,omitempty"`
	TemplateHTML    *string                 `json:"templateHtml,omitempty" bson:"templateHtml,omitempty"`
	TemplateCSS     *string                 `json:"templateCss,omitempty" bson:"templateCss,omitempty"`
	UIPrompt        *string                 `json:"uiPrompt,omitempty" bson:"uiPrompt,omitempty"`
	TemplateHistory []*TemplateHistoryEntry  `json:"templateHistory,omitempty" bson:"templateHistory,omitempty"`
	IsShared        bool                    `json:"isShared" bson:"isShared"`
	Searchable      bool                    `json:"searchable" bson:"searchable"`
	AllowContribute bool                    `json:"allowContribute" bson:"allowContribute"`
	Sharers         []*Sharer               `json:"sharers,omitempty" bson:"sharers,omitempty"`

	// CHART 專用
	ChartKind *string `json:"chartKind,omitempty" bson:"chartKind,omitempty"`
}

type Index struct {
	Name       string   `bson:"name"`
	Notes      []string `bson:"notes"`
	IsReserved bool     `bson:"isReserved"`
}

type CardFieldDef struct {
	Name    string   `json:"name" bson:"name"`
	Type    string   `json:"type" bson:"type"`
	Options []string `json:"options,omitempty" bson:"options,omitempty"`
}

type TemplateHistoryEntry struct {
	HTML      string `json:"html" bson:"html"`
	CSS       string `json:"css" bson:"css"`
	Timestamp string `json:"timestamp" bson:"timestamp"`
}

type Sharer struct {
	UserID string `json:"userId" bson:"userId"`
	Role   string `json:"role" bson:"role"`
}

// GetType 回傳 Folder type，nil 時回傳空字串
func (f *Folder) GetType() string {
	if f.Type == nil {
		return ""
	}
	return *f.Type
}
