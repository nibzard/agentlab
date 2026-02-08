package daemon

import (
	"net/http"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics collects Prometheus counters and histograms for agentlabd.
type Metrics struct {
	registry                *prometheus.Registry
	sandboxTransitionsTotal *prometheus.CounterVec
	sandboxProvisionSeconds prometheus.Histogram
	sandboxRevertTotal      *prometheus.CounterVec
	sandboxRevertSeconds    *prometheus.HistogramVec
	jobStatusTotal          *prometheus.CounterVec
	jobDurationSeconds      *prometheus.HistogramVec
}

// NewMetrics constructs a metrics registry and registers all collectors.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	sandboxTransitionsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "transitions_total",
			Help:      "Total number of sandbox state transitions.",
		},
		[]string{"from", "to"},
	)
	sandboxProvisionSeconds := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "provision_duration_seconds",
			Help:      "Time from sandbox creation to RUNNING.",
			Buckets:   []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1200},
		},
	)
	sandboxRevertTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "revert_total",
			Help:      "Total number of sandbox revert operations.",
		},
		[]string{"result"},
	)
	sandboxRevertSeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "revert_duration_seconds",
			Help:      "Time spent reverting sandboxes to the clean snapshot.",
			Buckets:   []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
		},
		[]string{"result"},
	)
	jobStatusTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "agentlab",
			Subsystem: "job",
			Name:      "status_total",
			Help:      "Total job status transitions.",
		},
		[]string{"status"},
	)
	jobDurationSeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "job",
			Name:      "duration_seconds",
			Help:      "Job runtime from creation to final status.",
			Buckets:   []float64{5, 10, 30, 60, 120, 300, 600, 1200, 1800, 3600, 7200},
		},
		[]string{"status"},
	)

	registry.MustRegister(
		sandboxTransitionsTotal,
		sandboxProvisionSeconds,
		sandboxRevertTotal,
		sandboxRevertSeconds,
		jobStatusTotal,
		jobDurationSeconds,
	)

	return &Metrics{
		registry:                registry,
		sandboxTransitionsTotal: sandboxTransitionsTotal,
		sandboxProvisionSeconds: sandboxProvisionSeconds,
		sandboxRevertTotal:      sandboxRevertTotal,
		sandboxRevertSeconds:    sandboxRevertSeconds,
		jobStatusTotal:          jobStatusTotal,
		jobDurationSeconds:      jobDurationSeconds,
	}
}

// Handler returns an HTTP handler that serves the metrics registry.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) IncSandboxTransition(from, to models.SandboxState) {
	if m == nil {
		return
	}
	m.sandboxTransitionsTotal.WithLabelValues(string(from), string(to)).Inc()
}

func (m *Metrics) ObserveSandboxProvision(duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	m.sandboxProvisionSeconds.Observe(seconds)
}

func (m *Metrics) IncSandboxRevert(result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.sandboxRevertTotal.WithLabelValues(result).Inc()
}

func (m *Metrics) ObserveSandboxRevertDuration(result string, duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.sandboxRevertSeconds.WithLabelValues(result).Observe(seconds)
}

func (m *Metrics) IncJobStatus(status models.JobStatus) {
	if m == nil {
		return
	}
	m.jobStatusTotal.WithLabelValues(string(status)).Inc()
}

func (m *Metrics) ObserveJobDuration(status models.JobStatus, duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	m.jobDurationSeconds.WithLabelValues(string(status)).Observe(seconds)
}
