package model

// Card 卡片
type Card struct {
	ID            string  `json:"id"`
	ContributorID *string `json:"contributorId,omitempty"`
	ParentID      string  `json:"parentID"`
	Name          string  `json:"name"`
	Fields        *string `json:"fields,omitempty"`
	Reviews       *string `json:"reviews,omitempty"`
	Coordinates   *string `json:"coordinates,omitempty"`
	OrderAt       *string `json:"orderAt,omitempty"`
	IsDeleted     bool    `json:"isDeleted"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
	Version       int     `json:"usn"`
}
