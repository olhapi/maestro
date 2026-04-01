package claude

import (
	"context"
	"fmt"

	"github.com/olhapi/maestro/internal/agentruntime"
)

var stdioCapabilities = agentruntime.Capabilities{}

func Start(ctx context.Context, spec agentruntime.RuntimeSpec, observers agentruntime.Observers) (agentruntime.Client, error) {
	if spec.Provider != "" && spec.Provider != agentruntime.ProviderClaude {
		return nil, fmt.Errorf("%w: provider %q", agentruntime.ErrUnsupportedCapability, spec.Provider)
	}
	switch spec.Transport {
	case agentruntime.TransportStdio:
		return startStdio(spec, observers), nil
	default:
		return nil, fmt.Errorf("%w: transport %q", agentruntime.ErrUnsupportedCapability, spec.Transport)
	}
}
