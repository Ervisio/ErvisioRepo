package authz

import "testing"

func TestParseOrgRole(t *testing.T) {
	for _, s := range []string{"owner", "admin", "member", "billing"} {
		if r, err := ParseOrgRole(s); err != nil || string(r) != s {
			t.Fatalf("ParseOrgRole(%q) = %q, %v", s, r, err)
		}
	}
	for _, s := range []string{"", "Owner", "root", "maintainer"} {
		if _, err := ParseOrgRole(s); err == nil {
			t.Fatalf("ParseOrgRole(%q) accepted", s)
		}
	}
}

func TestOrgActor(t *testing.T) {
	cases := []struct {
		a                 Actor
		view, manage, del bool
	}{
		{Actor{Role: OrgOwner}, true, true, true},
		{Actor{Role: OrgAdmin}, true, true, false},
		{Actor{Role: OrgMember}, true, false, false},
		{Actor{Role: OrgBilling}, true, false, false},
		{Actor{}, false, false, false},
		{Actor{Role: "bogus"}, false, false, false},
		{Actor{InstanceAdmin: true}, true, true, true},
	}
	for _, c := range cases {
		if c.a.CanView() != c.view || c.a.CanManage() != c.manage || c.a.CanDelete() != c.del {
			t.Errorf("%+v: view=%v manage=%v delete=%v", c.a, c.a.CanView(), c.a.CanManage(), c.a.CanDelete())
		}
	}
}

func TestCanAssignRole(t *testing.T) {
	roles := []OrgRole{OrgNone, OrgOwner, OrgAdmin, OrgMember, OrgBilling}
	for _, actor := range []Actor{{Role: OrgOwner}, {Role: OrgAdmin}, {Role: OrgMember}, {Role: OrgBilling}, {}, {InstanceAdmin: true}} {
		for _, cur := range roles {
			for _, next := range roles {
				got := actor.CanAssignRole(cur, next)
				var want bool
				switch {
				case next == OrgNone:
					want = false
				case actor.InstanceAdmin || actor.Role == OrgOwner:
					want = true
				case actor.Role == OrgAdmin:
					want = cur != OrgOwner && next != OrgOwner
				}
				if got != want {
					t.Errorf("%+v assign %q->%q = %v, want %v", actor, cur, next, got, want)
				}
			}
		}
	}
	if (Actor{Role: OrgOwner}).CanAssignRole("bogus", OrgMember) {
		t.Fatal("unknown current role accepted")
	}
}

func TestCanRemoveMember(t *testing.T) {
	cases := []struct {
		a      Actor
		target OrgRole
		self   bool
		want   bool
	}{
		{Actor{Role: OrgOwner}, OrgOwner, false, true},
		{Actor{Role: OrgAdmin}, OrgOwner, false, false},
		{Actor{Role: OrgAdmin}, OrgAdmin, false, true},
		{Actor{Role: OrgAdmin}, OrgMember, false, true},
		{Actor{Role: OrgMember}, OrgMember, false, false},
		{Actor{Role: OrgMember}, OrgMember, true, true},
		{Actor{Role: OrgBilling}, OrgBilling, true, true},
		{Actor{}, OrgMember, false, false},
		{Actor{}, OrgMember, true, false},
		{Actor{InstanceAdmin: true}, OrgOwner, false, true},
		{Actor{Role: OrgOwner}, OrgNone, false, false},
	}
	for _, c := range cases {
		if got := c.a.CanRemoveMember(c.target, c.self); got != c.want {
			t.Errorf("%+v remove %q self=%v = %v, want %v", c.a, c.target, c.self, got, c.want)
		}
	}
}

func TestTeamRoles(t *testing.T) {
	if r, err := ParseTeamRole("maintainer"); err != nil || r != TeamMaintainer {
		t.Fatal(r, err)
	}
	if _, err := ParseTeamRole("owner"); err == nil {
		t.Fatal("owner accepted as team role")
	}
	cases := []struct {
		a    Actor
		team TeamRole
		want bool
	}{
		{Actor{Role: OrgAdmin}, TeamNone, true},
		{Actor{Role: OrgMember}, TeamMaintainer, true},
		{Actor{Role: OrgMember}, TeamMember, false},
		{Actor{}, TeamMaintainer, false},
	}
	for _, c := range cases {
		if got := c.a.CanManageTeamMembers(c.team); got != c.want {
			t.Errorf("%+v team %q = %v", c.a, c.team, got)
		}
	}
}
