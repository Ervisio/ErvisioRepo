package ghapi

import (
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/actions/artifacts"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// The interfaces below are the subsets of the services used by the API;
// the service interfaces of internal/httpapi satisfy them.

// Repos is the subset of the repository service used by the API.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	Create(ctx context.Context, who auth.Principal, in repos.CreateInput, c identity.Client) (repos.Repo, error)
	ListOwner(ctx context.Context, who auth.Principal, owner string, offset, limit int) ([]repos.Repo, error)
	ListMine(ctx context.Context, who auth.Principal, offset, limit int) ([]repos.Repo, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, in repos.UpdateInput, c identity.Client) (repos.Repo, error)
	Delete(ctx context.Context, who auth.Principal, owner, name string, c identity.Client) error
	ListCollaborators(ctx context.Context, who auth.Principal, owner, name string) ([]repos.Collaborator, error)
	Star(ctx context.Context, who auth.Principal, owner, name string) error
	Unstar(ctx context.Context, who auth.Principal, owner, name string) error
	IsStarred(ctx context.Context, who auth.Principal, owner, name string) (bool, error)
	ListStargazers(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]repos.Stargazer, error)
	ListStarred(ctx context.Context, who auth.Principal, username string, offset, limit int) ([]repos.StarredRepo, error)
}

// Orgs is the subset of the organization service used by the API.
type Orgs interface {
	GetOrg(ctx context.Context, who auth.Principal, name string) (orgs.Org, error)
	ListMyOrgs(ctx context.Context, who auth.Principal) ([]orgs.Org, error)
}

// Identity is the subset of the identity service used by the API.
type Identity interface {
	User(ctx context.Context, id uuid.UUID) (identity.User, error)
	SearchUsers(ctx context.Context, who auth.Principal, query string, repoID uuid.UUID, limit int) ([]identity.UserSummary, error)
}

// Issues is the subset of the issue service used by the API.
type Issues interface {
	List(ctx context.Context, who auth.Principal, owner, name string, in issues.ListInput) ([]issues.Issue, error)
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (issues.Issue, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in issues.CreateInput) (issues.Issue, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in issues.UpdateInput) (issues.Issue, error)

	ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]issues.Comment, error)
	GetComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Comment, error)
	CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, body string) (issues.Comment, error)
	UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (issues.Comment, error)
	DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
	Delete(ctx context.Context, who auth.Principal, owner, name string, number int64, c identity.Client) error
	Lock(ctx context.Context, who auth.Principal, owner, name string, number int64, reason string, c identity.Client) (issues.Issue, error)
	Unlock(ctx context.Context, who auth.Principal, owner, name string, number int64, c identity.Client) (issues.Issue, error)

	ListLabels(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]issues.Label, error)
	CreateLabel(ctx context.Context, who auth.Principal, owner, name string, in issues.LabelInput, c identity.Client) (issues.Label, error)
	UpdateLabel(ctx context.Context, who auth.Principal, owner, name, label string, in issues.LabelInput, c identity.Client) (issues.Label, error)
	DeleteLabel(ctx context.Context, who auth.Principal, owner, name, label string, c identity.Client) error

	ListMilestones(ctx context.Context, who auth.Principal, owner, name, state string, offset, limit int) ([]issues.Milestone, error)
	GetMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Milestone, error)
	CreateMilestone(ctx context.Context, who auth.Principal, owner, name string, in issues.MilestoneInput, c identity.Client) (issues.Milestone, error)
	UpdateMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in issues.MilestoneInput, c identity.Client) (issues.Milestone, error)
	DeleteMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
}

// Pulls is the subset of the pull request service used by the API.
type Pulls interface {
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (pulls.Pull, error)
	List(ctx context.Context, who auth.Principal, owner, name string, o pulls.ListOptions) ([]pulls.Pull, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in pulls.CreateInput) (pulls.Pull, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.UpdateInput) (pulls.Pull, error)
	ListCommits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]gitbrowse.Commit, bool, error)
	ListFiles(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]gitbrowse.FileChange, error)
	Merge(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.MergeInput) (string, error)
	Diff(ctx context.Context, who auth.Principal, owner, name string, number int64, paths []string) (gitbrowse.Diff, error)
	UpdateBranch(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.UpdateBranchInput) (string, error)

	ListReviews(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]pulls.Review, error)
	GetReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) (pulls.Review, error)
	CreateReview(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.ReviewInput) (pulls.Review, error)
	SubmitReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, event, body string) (pulls.Review, error)
	DeletePendingReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID) error
	DismissReview(ctx context.Context, who auth.Principal, owner, name string, number int64, id uuid.UUID, message string) (pulls.Review, error)
	RequestReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)
	RemoveReviewers(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewers []string) (pulls.Pull, error)

	ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, reviewID uuid.UUID, offset, limit int) ([]pulls.Comment, error)
	CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, in pulls.CommentInput) (pulls.Comment, error)
	UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (pulls.Comment, error)
	DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error

	CreateFork(ctx context.Context, who auth.Principal, owner, name string, in pulls.ForkInput, c identity.Client) (repos.Repo, error)
	ListForks(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]repos.Repo, error)
	ForkInfo(ctx context.Context, who auth.Principal, repoID uuid.UUID) (pulls.ForkOf, error)
	MergeUpstream(ctx context.Context, who auth.Principal, owner, name, branch string) (pulls.MergeUpstreamResult, error)
}

// Releases is the subset of the release service used by the API.
type Releases interface {
	List(ctx context.Context, who auth.Principal, owner, name, tag string, offset, limit int) ([]releases.Release, error)
	Get(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (releases.Release, error)
	Latest(ctx context.Context, who auth.Principal, owner, name string) (releases.Release, error)
	GenerateNotes(ctx context.Context, who auth.Principal, owner, name string, in releases.NotesInput) (releases.Notes, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in releases.CreateInput, c identity.Client) (releases.Release, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in releases.UpdateInput, c identity.Client) (releases.Release, error)
	Delete(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
	UploadAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID uuid.UUID, in releases.UploadInput, c identity.Client) (releases.Asset, error)
	UpdateAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, newName, label *string, c identity.Client) (releases.Asset, error)
	DeleteAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, c identity.Client) error
	GetAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID) (releases.Asset, error)
	OpenAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, rng *blob.Range) (io.ReadCloser, releases.Asset, error)
}

// Statuses is the subset of the commit status and checks service used by
// the API.
type Statuses interface {
	CreateStatus(ctx context.Context, who auth.Principal, owner, name, ref string, in statuses.StatusInput) (statuses.Status, error)
	ListStatuses(ctx context.Context, who auth.Principal, owner, name, ref string, offset, limit int) ([]statuses.Status, string, error)
	CombinedStatus(ctx context.Context, who auth.Principal, owner, name, ref string) (statuses.Combined, error)
	CreateCheckRun(ctx context.Context, who auth.Principal, owner, name string, in statuses.CheckRunInput) (statuses.CheckRun, error)
	UpdateCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, p statuses.CheckRunPatch) (statuses.CheckRun, error)
	GetCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (statuses.CheckRun, error)
	ListCheckRunAnnotations(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, offset, limit int) ([]statuses.Annotation, error)
	ListCheckRuns(ctx context.Context, who auth.Principal, owner, name, ref string, f statuses.CheckRunFilter) ([]statuses.CheckRun, int, string, error)
	ListSuiteCheckRuns(ctx context.Context, who auth.Principal, owner, name string, suiteID uuid.UUID, f statuses.CheckRunFilter) ([]statuses.CheckRun, int, error)
	GetCheckSuite(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (statuses.CheckSuite, error)
	ListCheckSuites(ctx context.Context, who auth.Principal, owner, name, ref, app string, offset, limit int) ([]statuses.CheckSuite, int, string, error)
}

// Actions is the subset of the Actions service used by the API; nil
// answers 404 on the Actions endpoints.
type Actions interface {
	ListWorkflows(ctx context.Context, who auth.Principal, owner, name string) ([]actions.Workflow, error)
	ListRuns(ctx context.Context, who auth.Principal, owner, name string, f actions.RunFilter) ([]actions.Run, int64, error)
	GetRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (actions.Run, error)
	ListJobs(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID, attempt int32) ([]actions.Job, error)
	GetJob(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (actions.Job, []actions.Step, error)
	RawLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (io.ReadCloser, error)
	CancelRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) error
	RerunRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (actions.Run, error)
	Dispatch(ctx context.Context, who auth.Principal, owner, name, workflow, ref string, inputs map[string]any) (dbgen.ActionsRun, error)

	RepoScope(ctx context.Context, who auth.Principal, owner, name, environment string) (actions.Scope, error)
	ListSecrets(ctx context.Context, sc actions.Scope) ([]actions.Secret, error)
	SetSecret(ctx context.Context, who auth.Principal, sc actions.Scope, name, value string) (actions.Secret, error)
	DeleteSecret(ctx context.Context, sc actions.Scope, name string) error
	ListVariables(ctx context.Context, sc actions.Scope) ([]actions.Variable, error)
	SetVariable(ctx context.Context, who auth.Principal, sc actions.Scope, name, value string) (actions.Variable, error)
	DeleteVariable(ctx context.Context, sc actions.Scope, name string) error
	SecretsPublicKey() (string, []byte, error)
	OpenSealedSecret(keyID string, sealed []byte) (string, error)
}

// Artifacts lists and serves the workflow artifacts; nil (no actions
// storage) answers empty lists and 404.
type Artifacts interface {
	List(ctx context.Context, repoID uuid.UUID, f artifacts.ListFilter) ([]artifacts.Artifact, int64, error)
	Get(ctx context.Context, repoID uuid.UUID, id int64) (artifacts.Artifact, error)
	DownloadURL(ctx context.Context, repoID uuid.UUID, id int64) (string, error)
	Delete(ctx context.Context, repoID uuid.UUID, id int64) error
}

// assetLinker is implemented by release services that can send asset
// downloads straight to the storage primary with a presigned URL.
type assetLinker interface {
	AssetURL(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, count bool) (string, releases.Asset, error)
}
