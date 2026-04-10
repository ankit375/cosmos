package configmgr

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	configPushesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloudctrl_config_pushes_total",
		Help: "Total config push attempts",
	}, []string{"result"}) // result: "success", "failed", "timeout"

	configSafeApplyInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cloudctrl_config_safe_apply_inflight",
		Help: "Number of in-flight safe-apply operations",
	})

	configReconcileTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cloudctrl_config_reconcile_runs_total",
		Help: "Total config reconciliation runs",
	})

	configReconcileDrift = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cloudctrl_config_reconcile_drift_total",
		Help: "Total devices with config drift found by reconciler",
	})

	configValidationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloudctrl_config_validation_total",
		Help: "Total config validations",
	}, []string{"result"}) // result: "valid", "invalid"
)
