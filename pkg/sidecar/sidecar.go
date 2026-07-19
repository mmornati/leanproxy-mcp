package sidecar

import (
	"context"
	"sync/atomic"

	"log/slog"
)

type RedactClient interface {
	Redact(ctx context.Context, content string) string
	FallbackCount() int64
	Provider() string
	Model() string
	Healthy(ctx context.Context) bool
}

type Manager struct {
	client        RedactClient
	enabled       atomic.Bool
	logger        *slog.Logger
}

func NewManager(cfg Config, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	client, err := NewClient(cfg, logger)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		client: client,
		logger: logger,
	}
	if client != nil {
		m.enabled.Store(true)
		logger.Info("sidecar: initialized",
			"provider", client.Provider(),
			"model", client.Model(),
		)
	} else {
		logger.Info("sidecar: disabled")
	}
	return m, nil
}

func (m *Manager) Enabled() bool {
	return m != nil && m.enabled.Load()
}

func (m *Manager) Redact(ctx context.Context, content string) string {
	if m == nil || !m.Enabled() {
		return content
	}
	return m.client.Redact(ctx, content)
}

func (m *Manager) FallbackCount() int64 {
	if m == nil || m.client == nil {
		return 0
	}
	return m.client.FallbackCount()
}

func (m *Manager) Provider() string {
	if m == nil || m.client == nil {
		return ""
	}
	return m.client.Provider()
}

func (m *Manager) Model() string {
	if m == nil || m.client == nil {
		return ""
	}
	return m.client.Model()
}

func (m *Manager) Healthy(ctx context.Context) bool {
	if m == nil || m.client == nil {
		return false
	}
	return m.client.Healthy(ctx)
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.enabled.Store(false)
	if c, ok := m.client.(*Client); ok {
		c.Close()
	}
}
