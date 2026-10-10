package repocfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || len(c.Setup) != 0 {
		t.Fatalf("sem arquivo = %+v %v", c, err)
	}
	body := `setup = ["cp ../.env .env"]
model = "sonnet"
target_branch = "develop"

[[tabs]]
label = "dev"
command = "npm run dev"
`
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Setup) != 1 || c.Model != "sonnet" || c.TargetBranch != "develop" || len(c.Tabs) != 1 || c.Tabs[0].Command != "npm run dev" {
		t.Fatalf("config = %+v", c)
	}
	if err := os.WriteFile(filepath.Join(dir, File), []byte("setup = ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("toml inválido deveria dar erro")
	}
}
