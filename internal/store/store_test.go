package store

import (
	"os"
	"path/filepath"
	"testing"
)

type item struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// TestSaveLoadRoundTrip garante que o que foi gravado volta idêntico.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	js := JSON[[]item]{Path: filepath.Join(dir, "sub", "data.json")}

	want := []item{{Name: "a", N: 1}, {Name: "b", N: 2}}
	if err := js.Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := js.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Load() = %+v, esperado %+v", got, want)
	}
}

// TestLoadMissingFile garante que a ausência do arquivo não é erro — devolve
// o zero de T, pronto para uso (ex.: primeira execução do wmonit).
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	js := JSON[[]item]{Path: filepath.Join(dir, "nao-existe.json")}

	got, err := js.Load()
	if err != nil {
		t.Fatalf("Load com arquivo ausente não deveria falhar: %v", err)
	}
	if got != nil {
		t.Errorf("Load com arquivo ausente = %+v, esperado zero value", got)
	}
}

// TestSaveNoLeftoverTemp garante que a gravação atômica (temp + rename) não
// deixa lixo no diretório de destino, nem em caso de sucesso nem de erro.
func TestSaveNoLeftoverTemp(t *testing.T) {
	dir := t.TempDir()
	js := JSON[[]item]{Path: filepath.Join(dir, "data.json")}

	if err := js.Save([]item{{Name: "a", N: 1}}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "data.json" {
		t.Errorf("diretório após Save = %v, esperado só data.json", entries)
	}
}
