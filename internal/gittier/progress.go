package gittier

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type observerKey struct{}

// WithObserver returns ctx carrying fn: a Hydrate call made with it (also
// indirectly, through gitstore.Tier.Acquire) reports its progress to fn
// until it returns. fn runs on another goroutine and must not block long.
func WithObserver(ctx context.Context, fn func(Event)) context.Context {
	return context.WithValue(ctx, observerKey{}, fn)
}

func observerFrom(ctx context.Context) func(Event) {
	fn, _ := ctx.Value(observerKey{}).(func(Event))
	return fn
}

// WithProgressWriter is WithObserver with a TextProgress writing to w (the
// stderr channel of an SSH session, a terminal).
func WithProgressWriter(ctx context.Context, w io.Writer, lang string) context.Context {
	return WithObserver(ctx, TextProgress(w, lang))
}

var progressMessages = map[string][3]string{
	// progress, done, failed
	"en": {"remote: fetching from remote storage… %d%%\r", "remote: fetching from remote storage… done.\n", "remote: fetching from remote storage failed.\n"},
	"it": {"remote: recupero dallo storage remoto… %d%%\r", "remote: recupero dallo storage remoto… fatto.\n", "remote: recupero dallo storage remoto non riuscito.\n"},
}

// TextProgress returns an observer printing git-style progress lines to w,
// such as "remote: fetching from remote storage… 42%". lang selects the
// language ("en" or "it", by prefix; English otherwise). Lines are written
// only when the percentage changes. Write errors are ignored: the client
// may have gone away.
func TextProgress(w io.Writer, lang string) func(Event) {
	msgs, ok := progressMessages[strings.ToLower(strings.SplitN(lang, "-", 2)[0])]
	if !ok {
		msgs = progressMessages["en"]
	}
	var mu sync.Mutex
	last := -1
	ended := false
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		if ended {
			return
		}
		switch e.Phase {
		case PhaseDone:
			ended = true
			_, _ = io.WriteString(w, msgs[1])
		case PhaseFailed:
			ended = true
			if last >= 0 {
				_, _ = io.WriteString(w, "\n")
			}
			_, _ = io.WriteString(w, msgs[2])
		default:
			if e.Percent != last {
				last = e.Percent
				_, _ = fmt.Fprintf(w, msgs[0], e.Percent)
			}
		}
	}
}
