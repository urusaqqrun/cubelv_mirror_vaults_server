package model

// Folder 統一容器（NOTE / TODO / CARD / CHART）
type Folder struct {
	ID         string  `json:"_id"`
	FolderName string  `json:"folderName"`
	Type       *string `json:"type,omitempty"`
	ParentID   *string `json:"parentID,omitempty"`
	OrderAt    *string `json:"orderAt,omitempty"`
	Icon       *string `json:"icon,omitempty"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
	Version    int     `json:"usn"`
	NoteNum    int64   `json:"noteNum"`
	IsTemp     bool    `json:"isTemp"`

	// NOTE/TODO 專用
	Indexes           []*Index  `json:"indexes,omitempty"`
	FolderSummary     *string   `json:"folderSummary,omitempty"`
	AiFolderName      *string   `json:"aiFolderName,omitempty"`
	AiFolderSummary   *string   `json:"aiFolderSummary,omitempty"`
	AiInstruction     *string   `json:"aiInstruction,omitempty"`
	AutoUpdateSummary bool      `json:"autoUpdateSummary,omitempty"`
	IsSummarizedNoteIds []*string `json:"isSummarizedNoteIds,omitempty"`

	// CARD 專用
	Fields          []*CardFieldDef         `json:"fields,omitempty"`
	TemplateHTML    *string                 `json:"templateHtml,omitempty"`
	TemplateCSS     *string                 `json:"templateCss,omitempty"`
	UIPrompt        *string                 `json:"uiPrompt,omitempty"`
	TemplateHistory []*TemplateHistoryEntry  `json:"templateHistory,omitempty"`
	IsShared        bool                    `json:"isShared"`
	Searchable      bool                    `json:"searchable"`
	AllowContribute bool                    `json:"allowContribute"`
	Sharers         []*Sharer               `json:"sharers,omitempty"`

	// CHART 專用
	ChartKind *string `json:"chartKind,omitempty"`
}

type Index struct {
	Name       string   `json:"name"`
	Notes      []string `json:"notes"`
	IsReserved bool     `json:"isReserved"`
}

type CardFieldDef struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

type TemplateHistoryEntry struct {
	HTML      string `json:"html"`
	CSS       string `json:"css"`
	Timestamp string `json:"timestamp"`
}

type Sharer struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

// GetType 回傳 Folder type，nil 時回傳空字串
func (f *Folder) GetType() string {
	if f.Type == nil {
		return ""
	}
	return *f.Type
}
