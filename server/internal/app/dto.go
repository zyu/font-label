package app

import "time"

type UserDTO struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	MustChange  bool       `json:"must_change_pwd"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ImageDTO struct {
	ID          int64            `json:"id"`
	FolderID    int64            `json:"folder_id"`
	Folder      string           `json:"folder"`
	StoredName  string           `json:"stored_name"`
	OrigName    string           `json:"orig_name"`
	Seq         int              `json:"seq"`
	Day         string           `json:"day"`
	Status      string           `json:"status"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	AnnotatorID *int64           `json:"annotator_id"`
	Annotator   string           `json:"annotator"`
	RejectCount int              `json:"reject_count"`
	ReviewCount int              `json:"review_count"`
	Kind        string           `json:"kind"`
	Total       *float64         `json:"total"`
	Grade       string           `json:"grade"`
	Comment     string           `json:"comment"`
	Scores      map[string]int   `json:"scores"`
	Exceptions  []string         `json:"exception_reasons"`
	DurationMS  *int             `json:"duration_ms"`
	ClaimedAt   *time.Time       `json:"claimed_at"`
	SubmittedAt *time.Time       `json:"submitted_at"`
	ThumbURL    string           `json:"thumb_url"`
	PreviewURL  string           `json:"preview_url"`
	OriginalURL string           `json:"original_url"`
}

type ReviewDTO struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	Comment     string    `json:"comment"`
	ReasonTags  []string  `json:"reason_tags"`
	Reviewer    string    `json:"reviewer"`
	ScorePatch  any       `json:"score_patch,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Version     int       `json:"annotation_version"`
}

type FolderDTO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	Creator   string    `json:"creator"`
	Archived  bool      `json:"archived"`
	Total     int       `json:"total"`
	Unassigned int      `json:"unassigned"`
	Assigned  int       `json:"assigned"`
	Submitted int       `json:"submitted"`
	Approved  int       `json:"approved"`
	Rejected  int       `json:"rejected"`
	Excluded  int       `json:"excluded"`
}
