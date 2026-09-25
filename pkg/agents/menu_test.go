package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		label    string
		expected model.OptionRole
	}{
		// allow_always
		{"Yes, and don't ask again for this command", model.RoleAllowAlways},
		{"Always allow access to /tmp", model.RoleAllowAlways},
		{"Allow for this session (shift+tab)", model.RoleAllowAlways},
		{"Accept all edits in project", model.RoleAllowAlways},
		{"Approve for all matching files", model.RoleAllowAlways},
		{"Switch to auto mode", model.RoleAllowAlways},
		{"Auto-approve file edits", model.RoleAllowAlways},

		// Priority check: "Yes" label with "don't ask again" must be allow_always, not allow_once
		{"Yes, and don't ask again", model.RoleAllowAlways},
		{"Yes, always allow", model.RoleAllowAlways},

		// allow_once
		{"Yes", model.RoleAllowOnce},
		{"yes, run command", model.RoleAllowOnce},
		{"Allow once", model.RoleAllowOnce},
		{"Approve this action", model.RoleAllowOnce},
		{"Proceed with execution", model.RoleAllowOnce},
		{"Accept change", model.RoleAllowOnce},

		// deny
		{"No", model.RoleDeny},
		{"no, tell me more", model.RoleDeny},
		{"Deny access", model.RoleDeny},
		{"Reject this edit", model.RoleDeny},
		{"Cancel command", model.RoleDeny},
		{"Decline proposal", model.RoleDeny},

		// choice
		{"Option A", model.RoleChoice},
		{"Rojo Red", model.RoleChoice},
		{"notes.txt Solo pasa a plural", model.RoleChoice},
		{"Chat about this", model.RoleChoice},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			got := classify(tc.label)
			if got != tc.expected {
				t.Errorf("classify(%q) = %v; want %v", tc.label, got, tc.expected)
			}
		})
	}
}

func TestKindFor(t *testing.T) {
	tests := []struct {
		name     string
		roles    []model.OptionRole
		expected model.PromptKind
	}{
		{
			name:     "permission with allow_once and deny",
			roles:    []model.OptionRole{model.RoleAllowOnce, model.RoleDeny},
			expected: model.PromptPermission,
		},
		{
			name:     "permission with allow_always and deny",
			roles:    []model.OptionRole{model.RoleAllowOnce, model.RoleAllowAlways, model.RoleDeny},
			expected: model.PromptPermission,
		},
		{
			name:     "question with choices only",
			roles:    []model.OptionRole{model.RoleChoice, model.RoleChoice, model.RoleChoice},
			expected: model.PromptQuestion,
		},
		{
			name:     "question with allow but no deny",
			roles:    []model.OptionRole{model.RoleAllowOnce, model.RoleChoice},
			expected: model.PromptQuestion,
		},
		{
			name:     "question with deny but no allow",
			roles:    []model.OptionRole{model.RoleDeny, model.RoleChoice},
			expected: model.PromptQuestion,
		},
		{
			name:     "empty options",
			roles:    nil,
			expected: model.PromptUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var opts []model.PromptOption
			for i, r := range tc.roles {
				opts = append(opts, model.PromptOption{ID: string(rune('1' + i)), Role: r})
			}
			got := kindFor(opts)
			if got != tc.expected {
				t.Errorf("kindFor() = %v; want %v", got, tc.expected)
			}
		})
	}
}

func TestArrowKeys(t *testing.T) {
	m := parsedMenu{
		Options: []menuOption{
			{Number: 1, Label: "Staging", Cursor: false},
			{Number: 2, Label: "Production", Cursor: true},
			{Number: 3, Label: "Canary", Cursor: false},
		},
	}

	// Option 1 is above cursor -> ["Up", "Enter"]
	k1 := arrowKeys(m.Options[0], 0, m)
	if !reflect.DeepEqual(k1, []string{"Up", "Enter"}) {
		t.Errorf("arrowKeys option 1 = %v; want [Up, Enter]", k1)
	}

	// Option 2 is at cursor -> ["Enter"]
	k2 := arrowKeys(m.Options[1], 1, m)
	if !reflect.DeepEqual(k2, []string{"Enter"}) {
		t.Errorf("arrowKeys option 2 = %v; want [Enter]", k2)
	}

	// Option 3 is below cursor -> ["Down", "Enter"]
	k3 := arrowKeys(m.Options[2], 2, m)
	if !reflect.DeepEqual(k3, []string{"Down", "Enter"}) {
		t.Errorf("arrowKeys option 3 = %v; want [Down, Enter]", k3)
	}
}

func TestDigitKeys(t *testing.T) {
	o := menuOption{Number: 3, Label: "Test"}
	k := digitKeys(o, 2, parsedMenu{})
	if !reflect.DeepEqual(k, []string{"3"}) {
		t.Errorf("digitKeys = %v; want [3]", k)
	}
}

func TestFindMenu_SyntheticCases(t *testing.T) {
	// 1. Cursor on option 2
	cursorData, err := os.ReadFile(filepath.Join("testdata", "generic", "cursor-option2.txt"))
	if err != nil {
		t.Fatalf("read cursor-option2.txt: %v", err)
	}
	m, ok := findMenu(string(cursorData))
	if !ok {
		t.Fatal("expected findMenu to succeed on cursor-option2.txt")
	}
	if len(m.Options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(m.Options))
	}
	if !m.Options[1].Cursor {
		t.Errorf("expected option 2 to have cursor")
	}
	k := arrowKeys(m.Options[2], 2, m)
	if !reflect.DeepEqual(k, []string{"Down", "Enter"}) {
		t.Errorf("expected option 3 arrowKeys to be [Down, Enter], got %v", k)
	}

	// 2. Continuation lines
	contData, err := os.ReadFile(filepath.Join("testdata", "generic", "continuation-lines.txt"))
	if err != nil {
		t.Fatalf("read continuation-lines.txt: %v", err)
	}
	m, ok = findMenu(string(contData))
	if !ok {
		t.Fatal("expected findMenu on continuation-lines.txt")
	}
	if len(m.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(m.Options))
	}
	if m.Options[0].Label != "First choice with extra explanation on line two" {
		t.Errorf("unexpected option 0 label: %q", m.Options[0].Label)
	}
	if m.Options[1].Label != "Second choice also with more details" {
		t.Errorf("unexpected option 1 label: %q", m.Options[1].Label)
	}

	// 3. Boxed menu
	boxData, err := os.ReadFile(filepath.Join("testdata", "generic", "boxed-menu.txt"))
	if err != nil {
		t.Fatalf("read boxed-menu.txt: %v", err)
	}
	m, ok = findMenu(string(boxData))
	if !ok {
		t.Fatal("expected findMenu on boxed-menu.txt")
	}
	if len(m.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(m.Options))
	}
	if m.Options[0].Label != "Yes" || m.Options[1].Label != "No" {
		t.Errorf("box characters not stripped from options: %q, %q", m.Options[0].Label, m.Options[1].Label)
	}
	if strings.ContainsAny(m.Title, "│┌┐└┘├┤") {
		t.Errorf("box characters in title: %q", m.Title)
	}

	// 4. Multi blocks - last block wins
	multiData, err := os.ReadFile(filepath.Join("testdata", "generic", "multi-blocks.txt"))
	if err != nil {
		t.Fatalf("read multi-blocks.txt: %v", err)
	}
	m, ok = findMenu(string(multiData))
	if !ok {
		t.Fatal("expected findMenu on multi-blocks.txt")
	}
	if len(m.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(m.Options))
	}
	if m.Options[0].Label != "Continue" || m.Options[1].Label != "Stop" {
		t.Errorf("expected last block (Continue, Stop), got: %q, %q", m.Options[0].Label, m.Options[1].Label)
	}

	// 5. No menu
	noMenuData, err := os.ReadFile(filepath.Join("testdata", "generic", "no-menu.txt"))
	if err != nil {
		t.Fatalf("read no-menu.txt: %v", err)
	}
	_, ok = findMenu(string(noMenuData))
	if ok {
		t.Fatal("expected findMenu to return false for no-menu.txt")
	}
}

func TestUnknownPrompt(t *testing.T) {
	screen := `line 1
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
line 10
line 11
line 12
line 13
line 14
`
	p := UnknownPrompt(screen)
	if p.Public.Kind != model.PromptUnknown {
		t.Errorf("expected Kind=unknown, got %v", p.Public.Kind)
	}
	if p.Public.Title != "" {
		t.Errorf("expected empty title, got %q", p.Public.Title)
	}
	if p.Public.Detail != "" {
		t.Errorf("expected empty detail, got %q", p.Public.Detail)
	}
	if p.Public.Options == nil || len(p.Public.Options) != 0 {
		t.Errorf("expected empty non-nil options, got %v", p.Public.Options)
	}
	tailLines := strings.Split(p.Public.RawTail, "\n")
	if len(tailLines) != 12 {
		t.Errorf("expected 12 raw_tail lines, got %d", len(tailLines))
	}
	if tailLines[0] != "line 3" || tailLines[11] != "line 14" {
		t.Errorf("unexpected raw_tail lines: first=%q, last=%q", tailLines[0], tailLines[11])
	}
	expectedFP := model.Fingerprint(model.PromptUnknown, "", "", nil)
	if p.Public.Fingerprint != expectedFP {
		t.Errorf("fingerprint mismatch: got %q, want %q", p.Public.Fingerprint, expectedFP)
	}
}

func labelsOf(m parsedMenu) []string {
	var out []string
	for _, o := range m.Options {
		out = append(out, o.Label)
	}
	return out
}

// A box indented from the left edge ("  │ 1. Yes │") is still a menu, and
// its labels carry no border characters.
func TestFindMenu_IndentedBox(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "generic", "indented-box.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findMenu(string(data))
	if !ok {
		t.Fatal("indented box: no menu found")
	}
	if got, want := labelsOf(m), []string{"Yes", "No"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
	if m.Title != "Delete the build cache?" {
		t.Errorf("title = %q", m.Title)
	}
}

// Real screen: OpenCode draws its question dialog inside an indented "┃"
// frame ("  ┃  1. red"). The descriptions under each option are continuation
// lines; the cwd drawn far to the right on the line after the last option is not.
func TestFindMenu_IndentedFrameRealScreen(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "opencode", "question-multiple.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findMenu(string(data))
	if !ok {
		t.Fatal("framed question: no menu found")
	}
	want := []string{"red Prefer red", "green Prefer green", "blue Prefer blue", "Type your own answer"}
	if got := labelsOf(m); !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
}

// A footer at the options' own indentation, with no blank line before it, is
// not a continuation of the last label ("No Esc to cancel").
func TestFindMenu_FooterWithoutBlankLine(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "generic", "footer-no-blank.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findMenu(string(data))
	if !ok {
		t.Fatal("no menu found")
	}
	if got, want := labelsOf(m), []string{"Yes", "No"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
}

// Real screen: Claude's plan approval prints a key hint under option 3; it is
// not part of the label (and "approve" in it would make the option allow_once).
func TestFindMenu_KeyHintIsNotLabel(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "claude", "plan-approval.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findMenu(string(data))
	if !ok {
		t.Fatal("no menu found")
	}
	want := []string{"Yes, and use auto mode", "Yes, manually approve edits", "Tell Claude what to change"}
	if got := labelsOf(m); !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %q, want %q", got, want)
	}
}

func TestClassify_TypographicApostrophe(t *testing.T) {
	if got := classify("Yes, and don’t ask again for: mv *"); got != model.RoleAllowAlways {
		t.Errorf("classify(don’t ask again) = %v, want allow_always", got)
	}
}

// Detail is capped at 400 characters on a rune boundary: the watch must never
// receive half a UTF-8 sequence (it would render U+FFFD).
func TestDetailTruncationKeepsUTF8(t *testing.T) {
	long := strings.Repeat("a", 399) + strings.Repeat("é", 10) // 'é' is 2 bytes: byte 400 splits one
	screen := "────────────────────\n Bash command\n\n   " + long +
		"\n\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel · Tab to amend\n"

	claudePrompt, ok := newClaudeAdapter(Config{}).ParsePrompt(screen)
	if !ok {
		t.Fatal("claude: no menu")
	}
	_, extracted := extractTitleAndDetail([]string{"Title", long, "1. Yes"}, 2)
	built := buildPrompt(parsedMenu{Title: "t", Detail: long, Options: []menuOption{{Number: 1, Label: "Yes"}, {Number: 2, Label: "No"}}}, digitKeys)

	for name, detail := range map[string]string{
		"claude":                claudePrompt.Public.Detail,
		"extractTitleAndDetail": extracted,
		"buildPrompt":           built.Public.Detail,
	} {
		if !utf8.ValidString(detail) {
			t.Errorf("%s: detail is not valid UTF-8", name)
		}
		if n := utf8.RuneCountInString(detail); n != maxDetailRunes {
			t.Errorf("%s: detail has %d runes, want %d", name, n, maxDetailRunes)
		}
		if !strings.HasSuffix(detail, "aé") {
			t.Errorf("%s: detail should end with one whole 'é', got tail %q", name, detail[len(detail)-3:])
		}
	}
	if built.Public.Fingerprint != model.Fingerprint(built.Public.Kind, "t", built.Public.Detail, []string{"Yes", "No"}) {
		t.Error("buildPrompt: fingerprint not computed over the truncated detail")
	}
}
