package ghapi

import (
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// classify maps a service error to its GitHub answer; nil means an
// unexpected error (500, logged).
func classify(err error) *apiError {
	e := func(status int, msg string) *apiError { return &apiError{status: status, msg: msg} }
	var (
		rv  *repos.ValidationError
		iv  *issues.ValidationError
		pv  *pulls.ValidationError
		lv  *releases.ValidationError
		ov  *orgs.ValidationError
		nm  *pulls.NotMergeableError
		mbe *http.MaxBytesError
	)
	switch {
	case errors.As(err, &rv):
		return e(http.StatusUnprocessableEntity, rv.Msg)
	case errors.As(err, &iv):
		return e(http.StatusUnprocessableEntity, iv.Msg)
	case errors.As(err, &pv):
		return e(http.StatusUnprocessableEntity, pv.Msg)
	case errors.As(err, &lv):
		return e(http.StatusUnprocessableEntity, lv.Msg)
	case errors.As(err, &ov):
		return e(http.StatusUnprocessableEntity, ov.Msg)
	case errors.As(err, &nm):
		msg := "Pull Request is not mergeable"
		if len(nm.Reasons) > 0 {
			msg += ": " + nm.Reasons[0]
		}
		return e(http.StatusMethodNotAllowed, msg)
	case errors.Is(err, pulls.ErrNotMergeable):
		return e(http.StatusMethodNotAllowed, "Pull Request is not mergeable")
	case errors.As(err, &mbe):
		return e(http.StatusRequestEntityTooLarge, "Request body is too large")
	case errors.Is(err, gitbrowse.ErrInvalid), errors.Is(err, git.ErrInvalid):
		return e(http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, repos.ErrNotFound), errors.Is(err, issues.ErrNotFound),
		errors.Is(err, issues.ErrCommentNotFound), errors.Is(err, issues.ErrLabelNotFound),
		errors.Is(err, issues.ErrMilestoneNotFound), errors.Is(err, pulls.ErrNotFound),
		errors.Is(err, pulls.ErrReviewNotFound), errors.Is(err, pulls.ErrCommentNotFound),
		errors.Is(err, releases.ErrNotFound), errors.Is(err, gitbrowse.ErrNotFound),
		errors.Is(err, gitbrowse.ErrEmpty), errors.Is(err, gitstore.ErrNotFound),
		errors.Is(err, orgs.ErrNotFound), errors.Is(err, identity.ErrNotFound):
		return e(http.StatusNotFound, "Not Found")
	case errors.Is(err, issues.ErrLocked):
		return e(http.StatusForbidden, "The conversation is locked")
	case quota.IsExceeded(err), errors.Is(err, gitbrowse.ErrQuota):
		return e(http.StatusForbidden, "Storage quota exceeded")
	case errors.Is(err, repos.ErrForbidden), errors.Is(err, orgs.ErrForbidden):
		return e(http.StatusForbidden, "Resource not accessible: you do not have permission to do this")
	case errors.Is(err, gitbrowse.ErrRejected):
		return e(http.StatusForbidden, err.Error())
	case errors.Is(err, repos.ErrNameTaken):
		return e(http.StatusUnprocessableEntity, "name already exists on this account")
	case errors.Is(err, issues.ErrLabelTaken), errors.Is(err, issues.ErrMilestoneTaken),
		errors.Is(err, releases.ErrTagTaken), errors.Is(err, releases.ErrNameTaken):
		return e(http.StatusUnprocessableEntity, "Validation Failed: "+err.Error())
	case errors.Is(err, gitbrowse.ErrConflict), errors.Is(err, pulls.ErrConflict):
		return e(http.StatusConflict, "Conflict: "+err.Error())
	case errors.Is(err, gitbrowse.ErrTooLarge):
		return e(http.StatusRequestEntityTooLarge, "Too large")
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
		return &apiError{status: http.StatusServiceUnavailable, msg: "Repository is not available yet, retry later",
			retryAfter: "30"}
	case errors.Is(err, pulls.ErrUnavailable), errors.Is(err, releases.ErrNoStorage),
		errors.Is(err, gitbrowse.ErrUnavailable), errors.Is(err, gitstore.ErrDegraded),
		errors.Is(err, gitstore.ErrHotMissing):
		return e(http.StatusServiceUnavailable, "Service Unavailable")
	}
	return nil
}
