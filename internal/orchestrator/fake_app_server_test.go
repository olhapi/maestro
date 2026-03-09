package orchestrator

import (
	"testing"

	"github.com/olhapi/maestro/internal/testutil/fakeappserver"
)

func TestFakeAppServerHelperProcess(t *testing.T) {
	fakeappserver.MaybeRun()
}
