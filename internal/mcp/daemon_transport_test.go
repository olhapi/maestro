package mcp

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	enableInMemoryDaemonTransport()
	os.Exit(m.Run())
}
