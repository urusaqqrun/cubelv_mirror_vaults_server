package model

// Chart 圖表
type Chart struct {
	ID       string  `json:"id"`
	ParentID string  `json:"parentID"`
	Name      string  `json:"name"`
	Data      *string `json:"data,omitempty"`
	IsDeleted bool    `json:"isDeleted"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	Version   int     `json:"usn"`
}
