package command

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	commandsQueuedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "queued_total",
		Help:      "Total commands queued",
	}, []string{"command_type"})

	commandsSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "sent_total",
		Help:      "Total commands sent to devices",
	}, []string{"command_type"})

	commandsCompletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "completed_total",
		Help:      "Total commands completed",
	}, []string{"command_type", "result"}) // result: "success", "failed"

	commandsExpiredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "expired_total",
		Help:      "Total commands expired (max retries exceeded or TTL)",
	}, []string{"command_type"})

	commandsRetriedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "retried_total",
		Help:      "Total command retries",
	}, []string{"command_type"})

	commandsInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "inflight",
		Help:      "Number of commands currently sent and awaiting ACK",
	})

	commandsQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "queue_depth",
		Help:      "Number of queued commands per device",
	}, []string{"device_id"})

	commandDispatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "dispatch_duration_seconds",
		Help:      "Time taken to dispatch a command to a device",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
	})

	commandTimeoutCheckDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "cloudctrl",
		Subsystem: "commands",
		Name:      "timeout_check_duration_seconds",
		Help:      "Duration of command timeout check runs",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25},
	})
)
