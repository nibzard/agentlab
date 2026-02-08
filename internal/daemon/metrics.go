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
	sandboxReadySeconds     prometheus.Histogram
	sandboxSSHSeconds       prometheus.Histogram
	sandboxStartSeconds     *prometheus.HistogramVec
	sandboxStopSeconds      *prometheus.HistogramVec
	sandboxDestroySeconds   *prometheus.HistogramVec
	sandboxRevertTotal      *prometheus.CounterVec
	sandboxRevertSeconds    *prometheus.HistogramVec
	sandboxIdleStopTotal    *prometheus.CounterVec
	jobStatusTotal          *prometheus.CounterVec
	jobDurationSeconds      *prometheus.HistogramVec
	jobTimeToStartSeconds   prometheus.Histogram
}

// NewMetrics constructs a metrics registry and registers all collectors.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	sloBuckets := []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1200}
	operationBuckets := []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300}

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
			Buckets:   sloBuckets,
		},
	)
	sandboxReadySeconds := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "time_to_ready_seconds",
			Help:      "Time from sandbox creation to READY.",
			Buckets:   sloBuckets,
		},
	)
	sandboxSSHSeconds := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "time_to_ssh_seconds",
			Help:      "Time from sandbox creation to SSH port readiness.",
			Buckets:   sloBuckets,
		},
	)
	sandboxStartSeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "start_duration_seconds",
			Help:      "Time spent starting a sandbox VM.",
			Buckets:   operationBuckets,
		},
		[]string{"result"},
	)
	sandboxStopSeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "stop_duration_seconds",
			Help:      "Time spent stopping a sandbox VM.",
			Buckets:   operationBuckets,
		},
		[]string{"result"},
	)
	sandboxDestroySeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "destroy_duration_seconds",
			Help:      "Time spent destroying a sandbox VM.",
			Buckets:   operationBuckets,
		},
		[]string{"result"},
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
			Buckets:   operationBuckets,
		},
		[]string{"result"},
	)
	sandboxIdleStopTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "agentlab",
			Subsystem: "sandbox",
			Name:      "idle_stop_total",
			Help:      "Total number of idle sandbox stops.",
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
	jobTimeToStartSeconds := prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "agentlab",
			Subsystem: "job",
			Name:      "time_to_start_seconds",
			Help:      "Time from job creation to RUNNING.",
			Buckets:   sloBuckets,
		},
	)

	registry.MustRegister(
		sandboxTransitionsTotal,
		sandboxProvisionSeconds,
		sandboxReadySeconds,
		sandboxSSHSeconds,
		sandboxStartSeconds,
		sandboxStopSeconds,
		sandboxDestroySeconds,
		sandboxRevertTotal,
		sandboxRevertSeconds,
		sandboxIdleStopTotal,
		jobStatusTotal,
		jobDurationSeconds,
		jobTimeToStartSeconds,
	)

	return &Metrics{
		registry:                registry,
		sandboxTransitionsTotal: sandboxTransitionsTotal,
		sandboxProvisionSeconds: sandboxProvisionSeconds,
		sandboxReadySeconds:     sandboxReadySeconds,
		sandboxSSHSeconds:       sandboxSSHSeconds,
		sandboxStartSeconds:     sandboxStartSeconds,
		sandboxStopSeconds:      sandboxStopSeconds,
		sandboxDestroySeconds:   sandboxDestroySeconds,
		sandboxRevertTotal:      sandboxRevertTotal,
		sandboxRevertSeconds:    sandboxRevertSeconds,
		sandboxIdleStopTotal:    sandboxIdleStopTotal,
		jobStatusTotal:          jobStatusTotal,
		jobDurationSeconds:      jobDurationSeconds,
		jobTimeToStartSeconds:   jobTimeToStartSeconds,
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

func (m *Metrics) ObserveSandboxReady(duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	m.sandboxReadySeconds.Observe(seconds)
}

func (m *Metrics) ObserveSandboxSSH(duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	m.sandboxSSHSeconds.Observe(seconds)
}

func (m *Metrics) ObserveSandboxStart(result string, duration time.Duration) {
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
	m.sandboxStartSeconds.WithLabelValues(result).Observe(seconds)
}

func (m *Metrics) ObserveSandboxStop(result string, duration time.Duration) {
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
	m.sandboxStopSeconds.WithLabelValues(result).Observe(seconds)
}

func (m *Metrics) ObserveSandboxDestroy(result string, duration time.Duration) {
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
	m.sandboxDestroySeconds.WithLabelValues(result).Observe(seconds)
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

func (m *Metrics) IncSandboxIdleStop(result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.sandboxIdleStopTotal.WithLabelValues(result).Inc()
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

func (m *Metrics) ObserveJobStart(duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		return
	}
	m.jobTimeToStartSeconds.Observe(seconds)
}
