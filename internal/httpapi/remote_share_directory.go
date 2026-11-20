package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/codeshare"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

const sharingDirectoryLimit = 10

func (p problemResponse) VisitSearchErvisioCodeRecipientsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) SearchErvisioCodeRecipients(ctx context.Context, req v1gen.SearchErvisioCodeRecipientsRequestObject) (v1gen.SearchErvisioCodeRecipientsResponseObject, error) {
	accountID, problem, err := s.sharingAccount(ctx, req.Params.AccountId)
	if err != nil || problem.status != 0 {
		return problem, err
	}
	prefix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Params.Q), "@"))
	if prefix == "" || !utf8.ValidString(prefix) || utf8.RuneCountInString(prefix) > 100 ||
		strings.ContainsRune(prefix, '@') || strings.ContainsFunc(prefix, unicode.IsControl) {
		return badRequest("enter a handle or name prefix"), nil
	}
	switch req.Params.Kind {
	case v1gen.RecipientOrg:
		orgs, err := s.shares.SearchGrantOrgs(ctx, accountID, prefix, sharingDirectoryLimit)
		if err != nil {
			return shareProblem(err)
		}
		out := v1gen.SearchErvisioCodeRecipients200JSONResponse{Recipients: make([]v1gen.ErvisioCodeRecipient, 0, len(orgs))}
		for _, org := range orgs {
			out.Recipients = append(out.Recipients, v1gen.ErvisioCodeRecipient{
				Id: org.ID, Kind: v1gen.RecipientOrg, Name: org.Name, DisplayName: org.DisplayName,
			})
		}
		return out, nil
	case v1gen.RecipientUser:
		return s.searchShareUsers(ctx, accountID, prefix)
	default:
		return badRequest("kind must be user or org"), nil
	}
}

func (s *v1Server) searchShareUsers(ctx context.Context, accountID uuid.UUID, prefix string) (v1gen.SearchErvisioCodeRecipientsResponseObject, error) {
	if s.identity == nil {
		return errIdentityUnavailable, nil
	}
	role, err := s.shares.DirectoryRole(ctx, accountID)
	if errors.Is(err, codeshare.ErrNotFound) {
		return errAuthRequired, nil
	}
	if err != nil {
		return nil, err
	}
	users, err := s.identity.SearchUsers(ctx, auth.Principal{UserID: accountID, Role: role}, prefix, uuid.Nil, sharingDirectoryLimit+1)
	if err != nil {
		return nil, err
	}
	out := v1gen.SearchErvisioCodeRecipients200JSONResponse{Recipients: make([]v1gen.ErvisioCodeRecipient, 0, min(len(users), sharingDirectoryLimit))}
	for _, user := range users {
		if user.ID == accountID {
			continue
		}
		out.Recipients = append(out.Recipients, v1gen.ErvisioCodeRecipient{
			Id: user.ID, Kind: v1gen.RecipientUser, Name: user.Username, DisplayName: user.DisplayName,
		})
		if len(out.Recipients) == sharingDirectoryLimit {
			break
		}
	}
	return out, nil
}
