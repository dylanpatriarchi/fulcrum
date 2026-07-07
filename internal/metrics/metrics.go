// Package metrics exposes Prometheus instrumentation for the proxy. All methods
// are safe to call on a nil *Metrics, so callers can stay metrics-agnostic and
// pass nil to disable instrumentation (e.g. in tests).
package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the proxy's Prometheus collectors on a private registry.
type Metrics struct {
	reg *prometheus.Registry

	requests  *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	retries   prometheus.Counter
	failovers *prometheus.CounterVec
	backendUp *prometheus.GaugeVec
	active    *prometheus.GaugeVec
}

// New builds the collectors and registers them on a fresh registry (plus the
// standard Go/process collectors).
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fulcrum_requests_total",
			Help: "Total proxied requests by backend and response status code.",
		}, []string{"backend", "method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "fulcrum_request_duration_seconds",
			Help:    "Proxied request latency in seconds by backend.",
			Buckets: prometheus.DefBuckets,
		}, []string{"backend"}),
		retries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fulcrum_retries_total",
			Help: "Total retry attempts made after an upstream failure.",
		}),
		failovers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fulcrum_failovers_total",
			Help: "Failover outcomes after the first attempt failed.",
		}, []string{"result"}),
		backendUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "fulcrum_backend_up",
			Help: "Backend health (1 = healthy, 0 = unhealthy).",
		}, []string{"backend"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "fulcrum_backend_active_connections",
			Help: "In-flight requests currently dispatched to each backend.",
		}, []string{"backend"}),
	}
	m.reg.MustRegister(
		m.requests, m.duration, m.retries, m.failovers, m.backendUp, m.active,
		collectors(),
	)
	return m
}

// Handler serves the metrics in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// ObserveRequest records a completed proxied request.
func (m *Metrics) ObserveRequest(backend, method string, code int, seconds float64) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(backend, method, strconv.Itoa(code)).Inc()
	m.duration.WithLabelValues(backend).Observe(seconds)
}

// IncRetry counts one retry attempt.
func (m *Metrics) IncRetry() {
	if m == nil {
		return
	}
	m.retries.Inc()
}

// IncFailover records a failover outcome ("recovered" when a retry succeeded,
// "exhausted" when the retry budget ran out).
func (m *Metrics) IncFailover(result string) {
	if m == nil {
		return
	}
	m.failovers.WithLabelValues(result).Inc()
}

// SetBackendUp reflects a backend's health state.
func (m *Metrics) SetBackendUp(backend string, up bool) {
	if m == nil {
		return
	}
	v := 0.0
	if up {
		v = 1
	}
	m.backendUp.WithLabelValues(backend).Set(v)
}

// SetActive reflects a backend's in-flight request count.
func (m *Metrics) SetActive(backend string, n int64) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(backend).Set(float64(n))
}

// collectors returns the standard Go runtime + process collectors bundled so the
// admin endpoint also exposes process health.
func collectors() prometheus.Collector {
	return collectorSet{
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	}
}

// collectorSet lets several collectors register as one.
type collectorSet []prometheus.Collector

func (cs collectorSet) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range cs {
		c.Describe(ch)
	}
}

func (cs collectorSet) Collect(ch chan<- prometheus.Metric) {
	for _, c := range cs {
		c.Collect(ch)
	}
}
