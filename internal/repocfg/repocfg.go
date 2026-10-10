package repocfg

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const File = ".wmonit.toml"

type Tab struct {
	Label   string `toml:"label"`
	Command string `toml:"command"`
}

type Config struct {
	Setup          []string `toml:"setup"`
	Tabs           []Tab    `toml:"tabs"`
	Model          string   `toml:"model"`
	PermissionMode string   `toml:"permission_mode"`
	Template       string   `toml:"template"`
	Editor         string   `toml:"editor"`
	TargetBranch   string   `toml:"target_branch"`
}

func Load(repo string) (Config, error) {
	var c Config
	if repo == "" {
		return c, nil
	}
	path := filepath.Join(repo, File)
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("lendo %s: %w", path, err)
	}
	return c, nil
}
