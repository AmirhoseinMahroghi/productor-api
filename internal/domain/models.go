package domain

import "time"

type Role string

const (
	RoleUser      Role = "user"
	RoleModerator Role = "moderator"
	RoleAdmin     Role = "admin"
)

type ProductStatus string

const (
	ProductDraft            ProductStatus = "draft"
	ProductSubmitted        ProductStatus = "submitted"
	ProductInReview         ProductStatus = "in_review"
	ProductChangesRequested ProductStatus = "changes_requested"
	ProductRejected         ProductStatus = "rejected"
	ProductPublished        ProductStatus = "published"
	ProductUnpublished      ProductStatus = "unpublished"
	ProductSuperseded       ProductStatus = "superseded"
)

type CommentStatus string

const (
	CommentPending   CommentStatus = "pending"
	CommentPublished CommentStatus = "published"
	CommentRejected  CommentStatus = "rejected"
)

type Link struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type Organization struct {
	ID           string    `json:"id,omitempty"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Logo         string    `json:"logo,omitempty"`
	Bio          string    `json:"bio"`
	Website      string    `json:"website"`
	Links        []Link    `json:"links"`
	ProductCount int       `json:"productCount"`
	Role         string    `json:"role,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
}

type Feature struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

type Media struct {
	Kind    string `json:"kind"`
	Src     string `json:"src"`
	Poster  string `json:"poster,omitempty"`
	Alt     string `json:"alt"`
	Caption string `json:"caption"`
}

type ProductPayload struct {
	Name         string    `json:"name"`
	Logo         string    `json:"logo"`
	Tagline      string    `json:"tagline"`
	Description  string    `json:"description"`
	MainCategory string    `json:"mainCategory"`
	Categories   []string  `json:"categories"`
	Platforms    []string  `json:"platforms"`
	Pricing      string    `json:"pricing"`
	OpenSource   bool      `json:"openSource"`
	RepoURL      string    `json:"repoUrl,omitempty"`
	Website      string    `json:"website"`
	Features     []Feature `json:"features"`
	Pros         []string  `json:"pros"`
	Cons         []string  `json:"cons"`
	Media        []Media   `json:"media,omitempty"`
	UserTags     []string  `json:"userTags,omitempty"`
	Similar      []string  `json:"similar"`
	Audience     string    `json:"audience"`
}

type ReviewEvent struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Note   string    `json:"note,omitempty"`
}

type VoteSummary struct {
	Up int `json:"up"`
}

type Product struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	ProductPayload
	Org          Organization  `json:"org"`
	Votes        VoteSummary   `json:"votes"`
	Status       ProductStatus `json:"status"`
	SubmittedAt  time.Time     `json:"submittedAt"`
	ReviewerNote string        `json:"reviewerNote,omitempty"`
	History      []ReviewEvent `json:"history"`
}

type User struct {
	ID            string    `json:"id"`
	Phone         string    `json:"phone"`
	Role          Role      `json:"role"`
	CreatedAt     time.Time `json:"createdAt"`
	AdminRoleID   string    `json:"adminRoleId,omitempty"`
	AdminRoleName string    `json:"adminRoleName,omitempty"`
	Permissions   []string  `json:"permissions,omitempty"`
}

type Comment struct {
	ID           string        `json:"id"`
	ProductSlug  string        `json:"productSlug"`
	Author       string        `json:"author"`
	Body         string        `json:"body"`
	Status       CommentStatus `json:"status"`
	CreatedAt    time.Time     `json:"createdAt"`
	RejectReason string        `json:"rejectReason,omitempty"`
	EditDeadline *time.Time    `json:"editDeadline,omitempty"`
}

type Notification struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	URL       string     `json:"url,omitempty"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Topic struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
	Tone  int    `json:"tone"`
	Icon  string `json:"icon"`
}
