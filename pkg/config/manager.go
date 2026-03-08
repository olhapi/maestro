package config

import (
	"log/slog"
	"sync"
)

type Manager struct {
	path    string
	mu      sync.RWMutex
	current *Workflow
	stamp   fileStamp
	lastErr error
}

func NewManager(repoPath string) (*Manager, error) {
	path := WorkflowPath(repoPath)
	workflow, err := LoadWorkflow(path)
	if err != nil {
		return nil, err
	}
	stamp, err := currentStamp(path)
	if err != nil {
		return nil, err
	}
	return &Manager{path: path, current: workflow, stamp: stamp}, nil
}

func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) Current() (*Workflow, error) {
	if _, err := m.Refresh(); err != nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.current != nil {
			return m.current, nil
		}
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current, nil
}

func (m *Manager) Refresh() (*Workflow, error) {
	stamp, err := currentStamp(m.path)
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastErr = err
		if m.current != nil {
			slog.Error("Workflow reload failed; keeping last known good workflow", "path", m.path, "error", err)
			return m.current, nil
		}
		return nil, err
	}

	m.mu.RLock()
	same := stamp == m.stamp
	current := m.current
	m.mu.RUnlock()
	if same {
		return current, nil
	}

	workflow, err := LoadWorkflow(m.path)
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastErr = err
		if m.current != nil {
			slog.Error("Workflow reload failed; keeping last known good workflow", "path", m.path, "error", err)
			return m.current, nil
		}
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = workflow
	m.stamp = stamp
	m.lastErr = nil
	return workflow, nil
}

func (m *Manager) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastErr
}
