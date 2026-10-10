//go:build !windows

package herdr

import (
	"context"
	"net"
)

func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
