package agentruntime

func ApplyPermissionConfig(client Client, config PermissionConfig) {
	if client == nil {
		return
	}
	if !client.Capabilities().SupportsRuntimePermissionUpdates() {
		return
	}
	client.UpdatePermissions(config)
}

func (c Capabilities) SupportsResume() bool {
	return c.Resume
}

func (c Capabilities) SupportsQueuedInteractions() bool {
	return c.QueuedInteractions
}

func (c Capabilities) SupportsPlanGating() bool {
	return c.PlanGating
}

func (c Capabilities) SupportsLocalImageInput() bool {
	return c.LocalImageInput
}

func (c Capabilities) SupportsDynamicTools() bool {
	return c.DynamicTools
}

func (c Capabilities) SupportsRuntimePermissionUpdates() bool {
	return c.RuntimePermissionUpdates
}
