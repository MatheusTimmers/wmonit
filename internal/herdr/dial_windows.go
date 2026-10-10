//go:build windows

package herdr

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	if !strings.HasPrefix(path, `\\.\pipe\`) {
		path = `\\.\pipe\` + path
	}
	return winio.DialPipeContext(ctx, path)
}
