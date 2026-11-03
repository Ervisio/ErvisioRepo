package authz

import (
	"testing"
)

func FuzzParsePermission(f *testing.F) {
	for _, s := range []string{"none", "read", "admin", "", "READ", "write\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePermission(s)
		if err != nil {
			if p != PermNone {
				t.Fatalf("error with permission %v", p)
			}
			return
		}
		if p < PermNone || p > PermAdmin || p.String() != s {
			t.Fatalf("ParsePermission(%q) = %v", s, p)
		}
	})
}

func FuzzParseOrgRole(f *testing.F) {
	for _, s := range []string{"owner", "admin", "member", "billing", "", "Owner"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseOrgRole(s)
		if (err == nil) != r.Valid() || (err == nil && string(r) != s) {
			t.Fatalf("ParseOrgRole(%q) = %q, %v", s, r, err)
		}
	})
}

func factsFrom(flags uint16, vis, role, base, collab uint8, teams []byte) RepoFacts {
	ts := make([]Permission, 0, len(teams))
	for _, b := range teams {
		ts = append(ts, Permission(int8(b)))
	}
	return RepoFacts{
		Signed: flags&1 != 0, InstanceAdmin: flags&2 != 0, Restricted: flags&4 != 0, Suspended: flags&8 != 0,
		PersonalOwner:  flags&16 != 0,
		Visibility:     []Visibility{Public, Internal, Private, ""}[vis%4],
		OrgRole:        []OrgRole{OrgNone, OrgOwner, OrgAdmin, OrgMember, OrgBilling, "x"}[role%6],
		BasePermission: Permission(int8(base)), Collaborator: Permission(int8(collab)), Teams: ts,
	}
}

// FuzzRepoPermission checks the invariants of the effective permission: it is
// within range, anonymous callers never exceed read, suspended accounts get
// nothing, and adding a grant never lowers it.
func FuzzRepoPermission(f *testing.F) {
	f.Add(uint16(1), uint8(0), uint8(3), uint8(2), uint8(0), []byte{1, 4})
	f.Add(uint16(0), uint8(1), uint8(1), uint8(5), uint8(5), []byte{})
	f.Add(uint16(9), uint8(0), uint8(2), uint8(0), uint8(0), []byte{200})
	f.Fuzz(func(t *testing.T, flags uint16, vis, role, base, collab uint8, teams []byte) {
		if len(teams) > 64 {
			teams = teams[:64]
		}
		fa := factsFrom(flags, vis, role, base, collab, teams)
		p := RepoPermission(fa)
		if p < PermNone || p > PermAdmin {
			t.Fatalf("out of range %v for %+v", p, fa)
		}
		if !fa.Signed && p > PermRead {
			t.Fatalf("anonymous got %v", p)
		}
		if fa.Suspended && p != PermNone {
			t.Fatalf("suspended got %v", p)
		}
		if fa.OrgRole == OrgBilling && !fa.InstanceAdmin && !fa.PersonalOwner && fa.Visibility == Private &&
			fa.Collaborator <= PermNone && len(fa.Teams) == 0 && p != PermNone {
			t.Fatalf("billing got %v", p)
		}
		more := fa
		more.Teams = append(append([]Permission{}, fa.Teams...), Permission(int8(collab^base)))
		if RepoPermission(more) < p {
			t.Fatalf("adding a team grant lowered the permission")
		}
		more = fa
		more.Collaborator = max(fa.Collaborator, Permission(int8(base)))
		if RepoPermission(more) < p {
			t.Fatalf("raising the collaborator grant lowered the permission")
		}
		for a := range actionPerm {
			if Allowed(p, a) != (p >= actionPerm[a]) {
				t.Fatalf("Allowed mismatch")
			}
		}
	})
}

// FuzzAssignRole checks that no actor but an owner ever touches ownership.
func FuzzAssignRole(f *testing.F) {
	f.Add("admin", "member", "owner", false)
	f.Add("admin", "owner", "member", false)
	f.Fuzz(func(t *testing.T, actor, cur, next string, instanceAdmin bool) {
		a := Actor{Role: OrgRole(actor), InstanceAdmin: instanceAdmin}
		ok := a.CanAssignRole(OrgRole(cur), OrgRole(next))
		if ok && !instanceAdmin && OrgRole(actor) != OrgOwner && (OrgRole(cur) == OrgOwner || OrgRole(next) == OrgOwner) {
			t.Fatalf("%q changed ownership %q->%q", actor, cur, next)
		}
		if ok && !OrgRole(next).Valid() {
			t.Fatalf("assigned invalid role %q", next)
		}
		if ok && !a.CanManage() {
			t.Fatalf("non-manager %q assigned a role", actor)
		}
	})
}
