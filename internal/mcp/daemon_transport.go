package mcp

import (
	"fmt"
	"sync/atomic"
)

const (
	daemonTransportHTTP      = "http"
	daemonTransportInProcess = "in_process"
)

var useInMemoryDaemonTransport bool

var inMemoryDaemonBasePort atomic.Uint32

func enableInMemoryDaemonTransport() {
	useInMemoryDaemonTransport = true
}

func nextInMemoryDaemonBaseURL() string {
	port := 20000 + int(inMemoryDaemonBasePort.Add(1))
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}
