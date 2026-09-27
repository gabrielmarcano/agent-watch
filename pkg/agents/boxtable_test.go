package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real Claude Code screen: the markdown table of the reply drawn with box
// characters, a separator under every row and cells wrapped over two lines.
// The history item carries it as the markdown table the transcript has.
func TestScreenTurnFor_ClaudeBoxTable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "claude", "table-box.txt"))
	if err != nil {
		t.Fatal(err)
	}
	item := ScreenTurnFor(NewRegistry(Config{}).For("claude"), string(data))
	want := strings.Join([]string{
		"Este es un reparto en seis carriles para un equipo pequeño que construye una app web.",
		"",
		"| Carril | Tareas | Por qué juntas | Horas |",
		"| --- | --- | --- | --- |",
		"| Autenticación | Login, registro, recuperación de contraseña | Comparten la tabla de usuarios y el middleware de sesión; separarlas duplica lógica | 16 |",
		"| Base de datos | Esquema, migraciones, datos semilla | Todas tocan el esquema; un solo dueño evita migraciones en conflicto | 10 |",
		"| API | Endpoints CRUD, validación, manejo de errores | Usan los mismos tipos y el mismo formato de error; conviene definirlos una vez | 20 |",
		"| Frontend | Componentes base, rutas, formularios | Los formularios dependen de los componentes base y de las rutas ya montadas | 24 |",
		"| Pagos | Checkout, webhooks, facturas | Los webhooks confirman el cobro y disparan la factura; es un flujo único | 14 |",
		"| Calidad y despliegue | CI, tests e2e, despliegue | El pipeline ejecuta los tests e2e antes de desplegar; se ajustan en conjunto | 12 |",
		"",
		"El carril de base de datos va primero porque los demás dependen de su esquema, y el total ronda las 96 horas.",
	}, "\n")
	if item.Response != want {
		t.Errorf("response:\n%s\nwant:\n%s", item.Response, want)
	}
}

func TestBoxTables(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "separator under the header only; an empty first cell continues the row",
			in: []string{
				"╭──────┬──────────╮",
				"│ Name │ Note     │",
				"├──────┼──────────┤",
				"│ a    │ one      │",
				"│      │ wrapped  │",
				"│ b    │ two      │",
				"╰──────┴──────────╯",
			},
			want: []string{"| Name | Note |", "| --- | --- |", "| a | one wrapped |", "| b | two |"},
		},
		{
			name: "no separator: the first line is the header",
			in:   []string{"┌───┬───┐", "│ k │ v │", "│ x │ 1 │", "└───┴───┘"},
			want: []string{"| k | v |", "| --- | --- |", "| x | 1 |"},
		},
		{
			name: "a one-column dialog box stays",
			in:   []string{"╭────────────────╮", "│ Proceed?       │", "│ ❯ Yes          │", "╰────────────────╯"},
			want: []string{"╭────────────────╮", "│ Proceed?       │", "│ ❯ Yes          │", "╰────────────────╯"},
		},
		{
			name: "a row that does not match the border stays",
			in:   []string{"┌───┬───┐", "│ a b c │", "└───┴───┘"},
			want: []string{"┌───┬───┐", "│ a b c │", "└───┴───┘"},
		},
		{
			name: "no bottom border: the table ends at its last row",
			in:   []string{"  ┌───┬───┐", "  │ h │ i │", "  ├───┼───┤", "  │ 1 │ 2 │", "", "done"},
			want: []string{"  | h | i |", "  | --- | --- |", "  | 1 | 2 |", "", "done"},
		},
		{
			name: "wide characters and a pipe inside a cell",
			in:   []string{"┌──────┬────────────┐", "│ Op   │ Allowed    │", "├──────┼────────────┤", "│ a|b  │ ❌ never   │", "└──────┴────────────┘"},
			want: []string{"| Op | Allowed |", "| --- | --- |", `| a\|b | ❌ never |`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := boxTables(c.in)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}
