package releases

import (
	"errors"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// isExit1 reports whether err is a git command that exited with status 1,
// which "rev-parse --verify --quiet" uses for a missing object.
func isExit1(err error) bool {
	var ee *sys.ExitError
	return errors.As(err, &ee) && ee.Code == 1
}
