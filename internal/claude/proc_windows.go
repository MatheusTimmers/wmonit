//go:build windows

package claude

import (
	"os"
	"os/exec"
	"strconv"
)

// No Windows o process group não ajuda; o taskkill /T cobre a árvore.
func setSysProcAttr(cmd *exec.Cmd) {}

// killTree derruba o processo e os filhos; cai no Kill simples se não der.
func killTree(p *os.Process) {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run(); err != nil {
		_ = p.Kill()
	}
}
