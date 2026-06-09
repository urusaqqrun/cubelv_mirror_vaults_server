package model

// Note 筆記 / 待辦
type Note struct {
	ID       string   `json:"_id,omitempty"`
	Title    *string  `json:"title"`
	Content   *string  `json:"content"`
	Tags      []string `json:"tags"`
	ParentID  string   `json:"parentID"`
	Type      string   `json:"_type,omitempty"`
	CreateAt  int64    `json:"createAt"`
	UpdateAt  int64    `json:"updateAt"`
	OrderAt   *string  `json:"orderAt,omitempty"`
	Version   int      `json:"usn"`
	Status    *string  `json:"status,omitempty"`
	AiTitle   *string  `json:"aiTitle,omitempty"`
	AiTags    []string `json:"aiTags,omitempty"`
	ImgURLs   []string `json:"imgURLs"`
	IsNew     bool     `json:"isNew"`
}

// GetTitle 回傳筆記標題，nil 時回傳 "Untitled"
func (n *Note) GetTitle() string {
	if n.Title == nil || *n.Title == "" {
		return "Untitled"
	}
	return *n.Title
}

// GetContent 回傳筆記內容（HTML），nil 時回傳空字串
func (n *Note) GetContent() string {
	if n.Content == nil {
		return ""
	}
	return *n.Content
}
