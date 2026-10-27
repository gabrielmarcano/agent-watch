package agents

import "testing"

// A multi-question AskUserQuestion dialog (Claude Code 2.1.301) heads the
// screen with a tab bar; the title is the question under it, never the bar.
// The divider under the dialog carries the conversation's name.
const questionTabsScreen = `⏺ I have what I need. I'm asking you four decisions.

────────────────────────────────────────────────────────────────────────────────
←  ☒ Nombre  ☒ Vistas rol  ☐ Estados  ☐ Lógica CRM  ✔ Submit  →

Estados dentro del Rol: ¿cómo lo hacemos para el viernes?

❯ 1. Solo cambia la pantalla (Recomendado)
     La celda Rol enseña cada rol con su estado y se edita desde ahí.
  2. Migrar el dato al Rol
     El estado pasa a guardarse dentro del valor del Rol.
  3. Type something.
────────────────────────────────────────────────────────────────────────────────
  4. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel

───────────────────────────────────────────────── rename-note-to-notes ─
`

func TestClaudeQuestionDialogTabBarIsNotTheTitle(t *testing.T) {
	p, ok := newClaudeAdapter(Config{}).ParsePrompt(questionTabsScreen)
	if !ok {
		t.Fatal("the dialog was not parsed as a prompt")
	}
	if want := "Estados dentro del Rol: ¿cómo lo hacemos para el viernes?"; p.Public.Title != want {
		t.Errorf("Title = %q, want %q", p.Public.Title, want)
	}
	if p.Public.Kind != "question" {
		t.Errorf("Kind = %q, want question", p.Public.Kind)
	}
	var labels []string
	for _, o := range p.Public.Options {
		labels = append(labels, o.Label)
	}
	// "Type something." opens a text field the watch cannot fill.
	want := []string{"Solo cambia la pantalla (Recomendado)", "Migrar el dato al Rol", "Chat about this"}
	if len(labels) != len(want) {
		t.Fatalf("options = %v, want %v", labels, want)
	}
	for i, l := range want {
		if labels[i] != l {
			t.Errorf("option[%d] = %q, want %q", i, labels[i], l)
		}
	}
	if got := p.Keys["opt-1"]; len(got) != 1 || got[0] != "1" {
		t.Errorf("opt-1 keys = %v, want [1]", got)
	}
}
