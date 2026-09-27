package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFormat(t *testing.T) {
	const rev = "c8aa72e1f0b2d3c4e5f60718293a4b5c6d7e8f90"
	vcs := func(revision, modified string) []debug.BuildSetting {
		s := []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "-ldflags", Value: "-s -w"}}
		if revision != "" {
			s = append(s, debug.BuildSetting{Key: "vcs.revision", Value: revision})
		}
		if modified != "" {
			s = append(s, debug.BuildSetting{Key: "vcs.modified", Value: modified})
		}
		return s
	}
	cases := []struct {
		name     string
		version  string
		settings []debug.BuildSetting
		want     string
	}{
		{"clean build", "0.3.0", vcs(rev, "false"), "0.3.0 (c8aa72e)"},
		{"uncommitted changes", "0.3.0", vcs(rev, "true"), "0.3.0 (c8aa72e, modified)"},
		{"no modified flag", "0.3.0", vcs(rev, ""), "0.3.0 (c8aa72e)"},
		{"short revision kept whole", "0.3.0", vcs("abc12", "false"), "0.3.0 (abc12)"},
		{"no VCS stamp", "0.3.0", nil, "0.3.0"},
		{"modified without a revision", "0.3.0", vcs("", "true"), "0.3.0"},
		{"no version stamped", "", vcs(rev, "false"), "dev (c8aa72e)"},
		{"nothing at all", "", nil, "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.version, tc.settings); got != tc.want {
				t.Errorf("Format(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

// Test binaries carry no VCS stamp, so String is the bare version there.
func TestString_UsesThisBinarysBuildInfo(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	var settings []debug.BuildSetting
	if ok {
		settings = info.Settings
	}
	if got, want := String("1.2.3"), Format("1.2.3", settings); got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}
