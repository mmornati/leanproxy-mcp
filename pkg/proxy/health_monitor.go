package proxy

import (
	"time"
)

// ServerHealthStatus and the ServerStatus/ServerStatusList types below are
// the shared status vocabulary for `leanproxy status`: they are populated by
// cmd/status.go from the pool's live server state (not by an in-package
// health monitor — the polling implementation that used to live in this
// package was never wired into any command and was removed; see issue #303).

type ServerHealthStatus string

const (
	StatusRunning      ServerHealthStatus = "running"
	StatusError        ServerHealthStatus = "error"
	StatusStopped      ServerHealthStatus = "stopped"
	StatusStarting     ServerHealthStatus = "starting"
	StatusUnresponsive ServerHealthStatus = "unresponsive"
)

type ServerStatus struct {
	Name             string             `json:"name"`
	Status           ServerHealthStatus `json:"status"`
	Uptime           time.Duration      `json:"uptime"`
	LastResponseTime time.Time          `json:"last_response_time"`
	LastError        string             `json:"last_error,omitempty"`
	RestartCount     int                `json:"restart_count"`
	RequestCount     int64              `json:"request_count"`
	ErrorRate        float64            `json:"error_rate"`
	MemoryMB         int64              `json:"memory_mb,omitempty"`
	CPUPercent       float64            `json:"cpu_percent,omitempty"`
}

type ServerStatusList struct {
	Timestamp time.Time      `json:"timestamp"`
	Servers   []ServerStatus `json:"servers"`
}
