package connection

import (
	"context"

	"kubevirt.io/client-go/log"
)

type closer interface {
	Close() error
}

func CloseWithContext(ctx context.Context, conn closer, ifaceName string) {
	const errFmt = "failed to close %q DHCP server connection: %v"

	if ctx == nil {
		log.Log.Errorf(errFmt, ifaceName, "context not found")
		return
	}

	<-ctx.Done()
	if conn == nil {
		log.Log.Errorf(errFmt, ifaceName, "connection not found, is it already been closed?")
		return
	}
	if err := conn.Close(); err != nil {
		log.Log.Errorf(errFmt, ifaceName, err)
	}
	log.Log.Infof("DHCP server %q connection been closed successfully", ifaceName)

	return
}
