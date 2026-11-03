package authz

import "testing"

func TestParsePermission(t *testing.T) {
	for p := PermNone; p <= PermAdmin; p++ {
		got, err := ParsePermission(p.String())
		if err != nil || got != p {
			t.Fatalf("round trip %v: %v %v", p, got, err)
		}
	}
	for _, s := range []string{"", "Read", "owner", "pull"} {
		if _, err := ParsePermission(s); err == nil {
			t.Fatalf("ParsePermission(%q) accepted", s)
		}
	}
	if _, err := ParseGrant("none"); err == nil {
		t.Fatal("none accepted as grant")
	}
	if p, err := ParseGrant("write"); err != nil || p != PermWrite {
		t.Fatal(p, err)
	}
	if Permission(42).String() != "Permission(42)" {
		t.Fatal(Permission(42).String())
	}
}

func TestRepoPermission(t *testing.T) {
	cases := []struct {
		name string
		f    RepoFacts
		want Permission
	}{
		{"anonymous private", RepoFacts{}, PermNone},
		{"anonymous internal", RepoFacts{Visibility: Internal}, PermNone},
		{"anonymous public", RepoFacts{Visibility: Public}, PermRead},
		{"anonymous ignores grants", RepoFacts{Visibility: Public, Collaborator: PermAdmin}, PermRead},
		{"signed private", RepoFacts{Signed: true, Visibility: Private}, PermNone},
		{"signed internal", RepoFacts{Signed: true, Visibility: Internal}, PermRead},
		{"restricted internal", RepoFacts{Signed: true, Restricted: true, Visibility: Internal}, PermNone},
		{"restricted public", RepoFacts{Signed: true, Restricted: true, Visibility: Public}, PermRead},
		{"instance admin", RepoFacts{Signed: true, InstanceAdmin: true}, PermAdmin},
		{"personal owner", RepoFacts{Signed: true, PersonalOwner: true}, PermAdmin},
		{"org owner", RepoFacts{Signed: true, OrgRole: OrgOwner}, PermAdmin},
		{"org admin", RepoFacts{Signed: true, OrgRole: OrgAdmin}, PermAdmin},
		{"org member base", RepoFacts{Signed: true, OrgRole: OrgMember, BasePermission: PermWrite}, PermWrite},
		{"org member base none", RepoFacts{Signed: true, OrgRole: OrgMember, BasePermission: PermNone}, PermNone},
		{"team max", RepoFacts{Signed: true, OrgRole: OrgMember, BasePermission: PermRead, Teams: []Permission{PermTriage, PermMaintain}}, PermMaintain},
		{"collaborator", RepoFacts{Signed: true, Collaborator: PermWrite}, PermWrite},
		{"outside org member base ignored", RepoFacts{Signed: true, BasePermission: PermAdmin}, PermNone},
		{"suspended", RepoFacts{Signed: true, Suspended: true, InstanceAdmin: true, Visibility: Public}, PermNone},
		{"out of range grant clamped", RepoFacts{Signed: true, Collaborator: 99}, PermAdmin},
	}
	for _, c := range cases {
		if got := RepoPermission(c.f); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		p    Permission
		a    Action
		want bool
	}{
		{PermRead, ActRead, true},
		{PermRead, ActTriage, false},
		{PermTriage, ActTriage, true},
		{PermTriage, ActPush, false},
		{PermWrite, ActMerge, true},
		{PermWrite, ActSettings, false},
		{PermMaintain, ActManageBranches, true},
		{PermMaintain, ActDelete, false},
		{PermAdmin, ActCollaborators, true},
		{PermAdmin, "unknown", false},
		{PermNone, ActRead, false},
	}
	for _, c := range cases {
		if got := Allowed(c.p, c.a); got != c.want {
			t.Errorf("Allowed(%v, %q) = %v", c.p, c.a, got)
		}
	}
}
