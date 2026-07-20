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

// Save grava v indentado, criando o diretório se preciso. A gravação é
// atômica: escreve num arquivo temporário no mesmo diretório e substitui o
// destino com rename — assim uma falha no meio do caminho (ou uma leitura
// concorrente) nunca encontra o arquivo pela metade.
func (s JSON[T]) Save(v T) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.Path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
