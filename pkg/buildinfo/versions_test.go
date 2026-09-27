package buildinfo

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// VERSIONS at the repo root is the one place component versions are
// written. Every reader (make, build.sh, deploy.sh, Gradle) parses it with
// make's rules, so the file keeps to the subset they all agree on: bare
// KEY=value lines and whole-line comments.
var (
	versionLine = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=([^\s#"'$\\]+)$`)
	semver      = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	// CFBundleShortVersionString: three integers, nothing else.
	bundleVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	// A version written into the manifest's build step by hand.
	hardCodedLdflag = regexp.MustCompile(`main\.version=[0-9]`)
)

func readVersions(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("../../VERSIONS")
	if err != nil {
		t.Fatalf("VERSIONS must exist at the repo root: %v", err)
	}
	defer f.Close()
	vs := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := versionLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("VERSIONS:%d: %q is not a bare KEY=value line (no spaces, quotes or trailing comment)", n, line)
			continue
		}
		if _, dup := vs[m[1]]; dup {
			t.Errorf("VERSIONS:%d: %s is set twice", n, m[1])
		}
		vs[m[1]] = m[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestVersionsFile(t *testing.T) {
	vs := readVersions(t)
	for _, k := range []string{"BRIDGE_VERSION", "RELAY_VERSION", "WEAROS_VERSION_NAME"} {
		if !semver.MatchString(vs[k]) {
			t.Errorf("%s = %q, want x.y.z (optionally -suffix)", k, vs[k])
		}
	}
	if !bundleVersion.MatchString(vs["MENUBAR_VERSION"]) {
		t.Errorf("MENUBAR_VERSION = %q, want x.y.z (a macOS bundle version)", vs["MENUBAR_VERSION"])
	}
	code, err := strconv.Atoi(vs["WEAROS_VERSION_CODE"])
	if err != nil || code < 1 || code > 2100000000 {
		t.Errorf("WEAROS_VERSION_CODE = %q, want an integer from 1 to 2100000000", vs["WEAROS_VERSION_CODE"])
	}
}

// herdr-plugin.toml cannot read VERSIONS: its version is kept equal to
// BRIDGE_VERSION by hand, and its build step reads the ldflags from VERSIONS.
func TestPluginManifestMatchesVersions(t *testing.T) {
	vs := readVersions(t)
	var manifest struct {
		Version string `toml:"version"`
		Build   []struct {
			Command []string `toml:"command"`
		} `toml:"build"`
	}
	if _, err := toml.DecodeFile("../../herdr-plugin.toml", &manifest); err != nil {
		t.Fatalf("herdr-plugin.toml: %v", err)
	}
	if manifest.Version != vs["BRIDGE_VERSION"] {
		t.Errorf("herdr-plugin.toml version = %q, VERSIONS BRIDGE_VERSION = %q: keep them equal", manifest.Version, vs["BRIDGE_VERSION"])
	}
	if len(manifest.Build) == 0 {
		t.Fatal("herdr-plugin.toml has no [[build]] step")
	}
	for i, b := range manifest.Build {
		cmd := strings.Join(b.Command, " ")
		if hardCodedLdflag.MatchString(cmd) {
			t.Errorf("[[build]] %d hard-codes the version: %s", i, cmd)
		}
		if !strings.Contains(cmd, "BRIDGE_VERSION") || !strings.Contains(cmd, "VERSIONS") {
			t.Errorf("[[build]] %d does not take BRIDGE_VERSION from VERSIONS: %s", i, cmd)
		}
	}
}
