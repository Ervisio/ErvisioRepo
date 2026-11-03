package events

import (
	"time"

	"github.com/google/uuid"
)

// StatusPayload is the payload of KindStatus events. SubjectID is the status id.
type StatusPayload struct {
	ID          uuid.UUID `json:"id"`
	SHA         string    `json:"sha"`
	Context     string    `json:"context"`
	State       string    `json:"state"` // error, failure, pending or success
	Description string    `json:"description,omitempty"`
	TargetURL   string    `json:"target_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// CheckSuitePayload describes a check suite, in KindCheckSuite events and
// inside CheckRunPayload. SubjectID of a KindCheckSuite event is the suite id.
type CheckSuitePayload struct {
	ID         uuid.UUID `json:"id"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch,omitempty"`
	App        string    `json:"app"`
	Status     string    `json:"status"`               // queued, in_progress or completed
	Conclusion string    `json:"conclusion,omitempty"` // set when completed
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CheckRunPayload is the payload of KindCheckRun events. SubjectID is the
// check run id.
type CheckRunPayload struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	HeadSHA     string            `json:"head_sha"`
	Status      string            `json:"status"`
	Conclusion  string            `json:"conclusion,omitempty"`
	ExternalID  string            `json:"external_id,omitempty"`
	DetailsURL  string            `json:"details_url,omitempty"`
	Title       string            `json:"title,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Annotations int               `json:"annotations,omitempty"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	Suite       CheckSuitePayload `json:"suite"`
}
