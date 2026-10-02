package filter

import (
	"strings"
	"testing"
)

func TestNothingToFilter(t *testing.T) {
	r, err := New(Patterns{})
	if err != nil || r != nil {
		t.Fatalf("New(empty) = %v, %v; want nil, nil", r, err)
	}
	if r.Active() || r.FiltersGroups() || !r.Group("x") || !r.Keep("x", "y") {
		t.Error("nil rules must keep everything")
	}
}

func TestInvalidExpressionNamesItsOption(t *testing.T) {
	for p, option := range map[Patterns]string{
		{Group: "("}:          "--group-regex",
		{Channel: "["}:        "--channel-regex",
		{GroupExclude: "*"}:   "--group-exclude-regex",
		{ChannelExclude: "("}: "--channel-exclude-regex",
	} {
		if _, err := New(p); err == nil || !strings.Contains(err.Error(), option) {
			t.Errorf("%+v: err = %v, want it to name %s", p, err, option)
		}
	}
}

func TestKeep(t *testing.T) {
	r, err := New(Patterns{
		Group:          "^(FR|UK) ",
		GroupExclude:   "Adult",
		Channel:        "HD",
		ChannelExclude: "(?i)backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Active() || !r.FiltersGroups() {
		t.Error("rules with patterns must be active")
	}

	for _, c := range []struct {
		group, channel string
		want           bool
	}{
		{"FR News", "TF1 HD", true},
		{"UK Sport", "BBC HD", true},
		{"US News", "CNN HD", false},        // group not included
		{"FR Adult", "X HD", false},         // group excluded
		{"FR News", "TF1 SD", false},        // channel not included
		{"FR News", "TF1 HD Backup", false}, // channel excluded, case aside
		{"", "TF1 HD", false},               // no group: not included
	} {
		if got := r.Keep(c.group, c.channel); got != c.want {
			t.Errorf("Keep(%q, %q) = %v, want %v", c.group, c.channel, got, c.want)
		}
	}

	if !r.Group("FR News") || r.Group("FR Adult") || r.Group("US News") {
		t.Error("Group must apply the group patterns alone")
	}
}

func TestOnlyChannelPatterns(t *testing.T) {
	r, err := New(Patterns{ChannelExclude: "^XXX"})
	if err != nil {
		t.Fatal(err)
	}
	if r.FiltersGroups() {
		t.Error("no group pattern: groups do not matter")
	}
	if !r.Keep("", "TF1") || r.Keep("Any", "XXX one") || !r.Group("whatever") {
		t.Error("an exclusion alone keeps everything else")
	}
}
