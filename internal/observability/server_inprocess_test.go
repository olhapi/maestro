package observability

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
)

var fakePortCounter atomic.Uint32

func TestMain(m *testing.M) {
	os.Setenv(inProcessServerEnv, "1")
	os.Exit(m.Run())
}

func nextFakeAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", 29000+int(fakePortCounter.Add(1)))
}
