package websocket

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ═══════════════════════════════════════════════════════════
	// Connection metrics
	// ═══════════════════════════════════════════════════════════

	wsConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "connections_active",
			Help:      "Number of active WebSocket device connections",
		},
	)

	wsConnectionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "connections_total",
			Help:      "Total number of WebSocket connections established",
		},
	)

	wsConnectionErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "connection_errors_total",
			Help:      "Total WebSocket connection errors",
		},
		[]string{"reason"},
	)

	wsHandshakeDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "handshake_duration_seconds",
			Help:      "Duration of WebSocket device authentication handshake",
			Buckets:   []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
	)

	// ═══════════════════════════════════════════════════════════
	// Message metrics
	// ═══════════════════════════════════════════════════════════

	wsMessagesReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "messages_received_total",
			Help:      "Total messages received from devices",
		},
		[]string{"channel", "msg_type"},
	)

	wsMessagesSent = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "messages_sent_total",
			Help:      "Total messages sent to devices",
		},
		[]string{"channel", "msg_type"},
	)

	wsMessageErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "message_errors_total",
			Help:      "Total message processing errors",
		},
		[]string{"msg_type", "error_type"},
	)

	wsMessageBytesReceived = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "message_bytes_received_total",
			Help:      "Total bytes received from devices",
		},
	)

	wsMessageBytesSent = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "message_bytes_sent_total",
			Help:      "Total bytes sent to devices",
		},
	)

	wsMessagesDropped = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "messages_dropped_total",
			Help:      "Total messages dropped (send channel full or rate limited)",
		},
		[]string{"reason"},
	)

	// ═══════════════════════════════════════════════════════════
	// Device state metrics
	// ═══════════════════════════════════════════════════════════

	wsDevicesByStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "by_status",
			Help:      "Number of devices by status",
		},
		[]string{"status"},
	)

	// ═══════════════════════════════════════════════════════════
	// Rate limiter metrics
	// ═══════════════════════════════════════════════════════════

	wsRateLimitRejects = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "rate_limit_rejects_total",
			Help:      "Total connection/message rate limit rejections",
		},
		[]string{"type"},
	)

	// ═══════════════════════════════════════════════════════════
	// Worker metrics
	// ═══════════════════════════════════════════════════════════

	wsStatePersistDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "state_persist_duration_seconds",
			Help:      "Duration of state persistence batch writes",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
	)

	wsStatePersistBatchSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "websocket",
			Name:      "state_persist_batch_size",
			Help:      "Number of devices per state persistence batch",
			Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		},
	)

	// ═══════════════════════════════════════════════════════════
	// Phase 4: Device lifecycle metrics (NEW)
	// ═══════════════════════════════════════════════════════════

	wsDeviceAdoptionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "adoptions_total",
			Help:      "Total device adoptions",
		},
		[]string{"method"}, // "manual" or "auto"
	)

	wsDeviceDecommissionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "decommissions_total",
			Help:      "Total device decommissions",
		},
	)

	wsDeviceStateTransitions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "state_transitions_total",
			Help:      "Total device state transitions",
		},
		[]string{"from", "to"},
	)

	wsHeartbeatsProcessed = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "heartbeats_processed_total",
			Help:      "Total heartbeats processed",
		},
	)

	wsOfflineDetections = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "offline_detections_total",
			Help:      "Total devices marked offline by detector",
		},
	)

	wsAutoAdoptionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "devices",
			Name:      "auto_adoptions_total",
			Help:      "Total devices auto-adopted",
		},
	)
)
