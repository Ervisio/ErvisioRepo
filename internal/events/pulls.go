package events

import "github.com/google/uuid"

// PullPayload is the payload of KindPull events. SubjectID is the issue id
// of the pull request. Only the fields relevant to the action are set.
type PullPayload struct {
	Number   int64  `json:"number"`
	Title    string `json:"title,omitempty"`
	State    string `json:"state,omitempty"` // open, closed or merged
	Draft    bool   `json:"draft,omitempty"`
	HeadRepo string `json:"head_repo,omitempty"` // repository id of the head
	HeadRef  string `json:"head_ref,omitempty"`
	HeadSHA  string `json:"head_sha,omitempty"`
	BaseRef  string `json:"base_ref,omitempty"`
	BaseSHA  string `json:"base_sha,omitempty"`
	// Before is the previous head of a synchronize, the previous base of a
	// retarget (edited).
	Before string `json:"before,omitempty"`
	// Merged pull requests.
	MergeCommit string `json:"merge_commit,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	// review_requested and review_request_removed.
	Reviewer *uuid.UUID `json:"reviewer,omitempty"`
}

// PullReviewPayload is the payload of KindPullReview events. SubjectID is
// the review id.
type PullReviewPayload struct {
	Number    int64     `json:"number"`
	PullID    uuid.UUID `json:"pull_id"`
	ReviewID  uuid.UUID `json:"review_id"`
	State     string    `json:"state"` // commented, approved, changes_requested or dismissed
	CommitSHA string    `json:"commit_sha,omitempty"`
}

// PullReviewCommentPayload is the payload of KindPullReviewComment events.
// SubjectID is the comment id.
type PullReviewCommentPayload struct {
	Number    int64      `json:"number"`
	PullID    uuid.UUID  `json:"pull_id"`
	CommentID uuid.UUID  `json:"comment_id"`
	ReviewID  *uuid.UUID `json:"review_id,omitempty"`
	InReplyTo *uuid.UUID `json:"in_reply_to,omitempty"`
	Path      string     `json:"path,omitempty"`
	Line      int        `json:"line,omitempty"`
}
