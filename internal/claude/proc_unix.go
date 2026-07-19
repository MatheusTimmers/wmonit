//go:build !windows

package claude

import (
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttr coloca o claude num process group próprio, para o Kill
// alcançar também os filhos que ele disparar (builds, git…).
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree mata o process group inteiro; cai no Kill simples se não der.
func killTree(p *os.Process) {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		_ = p.Kill()
	}
}
