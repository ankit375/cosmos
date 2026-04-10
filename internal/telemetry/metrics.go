package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ── Ingestion ──────────────────────────────────────────

	metricsReportsReceived = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "reports_received_total",
			Help:      "Total MetricsReport messages received from devices",
		},
	)

	metricsReportsInvalid = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "reports_invalid_total",
			Help:      "Total invalid MetricsReport messages rejected",
		},
	)

	metricsIngestDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "ingest_duration_seconds",
			Help:      "Duration of processing a single MetricsReport",
			Buckets:   []float64{.0001, .0005, .001, .005, .01, .05},
		},
	)

	// ── Buffer ─────────────────────────────────────────────

	bufferDeviceMetricsSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "buffer_device_metrics",
			Help:      "Number of device metric rows in active buffer",
		},
	)

	bufferRadioMetricsSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "buffer_radio_metrics",
			Help:      "Number of radio metric rows in active buffer",
		},
	)

	bufferClientEventsSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "buffer_client_events",
			Help:      "Number of client events in active buffer",
		},
	)

	bufferSwapTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "buffer_swaps_total",
			Help:      "Total buffer swap operations",
		},
	)

	// ── Flusher ────────────────────────────────────────────

	flushDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "flush_duration_seconds",
			Help:      "Duration of batch flush to database",
			Buckets:   []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
	)

	flushDeviceRows = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "flush_device_rows_total",
			Help:      "Total device metric rows flushed to database",
		},
	)

	flushRadioRows = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "flush_radio_rows_total",
			Help:      "Total radio metric rows flushed to database",
		},
	)

	flushErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "flush_errors_total",
			Help:      "Total flush errors by type",
		},
		[]string{"type"},
	)

	// ── Client tracking ────────────────────────────────────

	clientSessionsOpened = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "client_sessions_opened_total",
			Help:      "Total client sessions opened",
		},
	)

	clientSessionsClosed = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "client_sessions_closed_total",
			Help:      "Total client sessions closed",
		},
	)

	clientRoamsDetected = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "client_roams_detected_total",
			Help:      "Total client roam events detected",
		},
	)

	activeClientsGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "active_clients",
			Help:      "Total active clients across all devices",
		},
	)

	// ── Query ──────────────────────────────────────────────

	queryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "cloudctrl",
			Subsystem: "telemetry",
			Name:      "query_duration_seconds",
			Help:      "Duration of metrics queries",
			Buckets:   []float64{.01, .05, .1, .25, .5, 1, 2.5},
		},
		[]string{"resolution", "type"},
	)
)
