// Package store guarda a leitura/gravação de um valor em JSON num arquivo,
// o algoritmo que estava copiado em tasks, history e session.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// JSON persiste um valor do tipo T num arquivo JSON.
type JSON[T any] struct{ Path string }

// Load lê o arquivo. Ausência não é erro: devolve o zero de T (slice nil,
// pronto para uso). Erro de leitura ou de JSON volta com o valor zero.
func (s JSON[T]) Load() (T, error) {
	var v T
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("lendo %s: %w", s.Path, err)
	}
	return v, nil
}

// Save grava v indentado, criando o diretório se preciso.
func (s JSON[T]) Save(v T) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o644)
}
