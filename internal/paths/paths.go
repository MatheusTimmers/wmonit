// Package paths centraliza a convenção de onde ficam os dados do wmonit,
// para a regra não viver copiada em cada store (tasks, history, session).
package paths

import (
	"os"
	"path/filepath"
)

// DataDir é a pasta de dados do wmonit: $XDG_DATA_HOME/wmonit ou, sem ele,
// ~/.local/share/wmonit.
func DataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wmonit")
}

// DataFile devolve o caminho de um arquivo dentro da pasta de dados.
func DataFile(name string) string { return filepath.Join(DataDir(), name) }
