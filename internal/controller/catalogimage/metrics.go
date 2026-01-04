/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package catalogimage

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricsNamespace = "catalogimage"
	metricsSubsystem = "controller"
)

var (
	// CatalogImagesTotal tracks the total number of catalog images by phase
	CatalogImagesTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "images_total",
			Help:      "Total number of catalog images by phase and namespace",
		},
		[]string{"namespace", "phase"},
	)

	// VerificationDuration tracks the duration of registry verification operations
	VerificationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "verification_duration_seconds",
			Help:      "Duration of registry verification operations in seconds",
			Buckets:   []float64{0.1, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0},
		},
		[]string{"registry", "result"},
	)

	// RegistryAccessTotal tracks the total number of registry access attempts
	RegistryAccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "registry_access_total",
			Help:      "Total number of registry access attempts",
		},
		[]string{"registry", "result"},
	)

	// CircuitBreakerState tracks the state of circuit breakers by registry
	CircuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "circuit_breaker_state",
			Help:      "Current circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"registry"},
	)

	// ReconcileTotal tracks the total number of reconciliation attempts
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "reconcile_total",
			Help:      "Total number of reconciliation attempts",
		},
		[]string{"namespace", "result"},
	)

	// ReconcileDuration tracks the duration of reconciliation operations
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of reconciliation operations in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"namespace"},
	)

	// PublishTotal tracks the total number of catalog image publications
	PublishTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "publish_total",
			Help:      "Total number of catalog image publications",
		},
		[]string{"source", "result"},
	)

	// ImageSizeBytes tracks the size of images in the catalog
	ImageSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "image_size_bytes",
			Help:      "Size of images in the catalog in bytes",
			Buckets:   []float64{1e6, 1e7, 1e8, 5e8, 1e9, 2e9, 5e9, 1e10},
		},
		[]string{"architecture", "distro"},
	)

	// MultiArchImages tracks the number of multi-architecture images
	MultiArchImages = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: metricsSubsystem,
			Name:      "multi_arch_images_total",
			Help:      "Total number of multi-architecture images in the catalog",
		},
	)
)

func init() {
	// Register metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		CatalogImagesTotal,
		VerificationDuration,
		RegistryAccessTotal,
		CircuitBreakerState,
		ReconcileTotal,
		ReconcileDuration,
		PublishTotal,
		ImageSizeBytes,
		MultiArchImages,
	)
}

// CircuitStateToFloat converts a circuit breaker state to a float for metrics
func CircuitStateToFloat(state CircuitState) float64 {
	switch state {
	case CircuitClosed:
		return 0
	case CircuitOpen:
		return 1
	case CircuitHalfOpen:
		return 2
	default:
		return 0
	}
}

// MetricsRecorder provides helper methods for recording metrics
type MetricsRecorder struct{}

// NewMetricsRecorder creates a new MetricsRecorder
func NewMetricsRecorder() *MetricsRecorder {
	return &MetricsRecorder{}
}

// RecordVerificationSuccess records a successful verification
func (m *MetricsRecorder) RecordVerificationSuccess(registry string, durationSeconds float64) {
	VerificationDuration.WithLabelValues(registry, "success").Observe(durationSeconds)
	RegistryAccessTotal.WithLabelValues(registry, "success").Inc()
}

// RecordVerificationFailure records a failed verification
func (m *MetricsRecorder) RecordVerificationFailure(registry string, durationSeconds float64) {
	VerificationDuration.WithLabelValues(registry, "failure").Observe(durationSeconds)
	RegistryAccessTotal.WithLabelValues(registry, "failure").Inc()
}

// RecordReconcileSuccess records a successful reconciliation
func (m *MetricsRecorder) RecordReconcileSuccess(namespace string, durationSeconds float64) {
	ReconcileTotal.WithLabelValues(namespace, "success").Inc()
	ReconcileDuration.WithLabelValues(namespace).Observe(durationSeconds)
}

// RecordReconcileError records a failed reconciliation
func (m *MetricsRecorder) RecordReconcileError(namespace string, durationSeconds float64) {
	ReconcileTotal.WithLabelValues(namespace, "error").Inc()
	ReconcileDuration.WithLabelValues(namespace).Observe(durationSeconds)
}

// RecordPublish records a catalog image publication
func (m *MetricsRecorder) RecordPublish(source, result string) {
	PublishTotal.WithLabelValues(source, result).Inc()
}

// UpdateCircuitBreakerState updates the circuit breaker state metric
func (m *MetricsRecorder) UpdateCircuitBreakerState(registry string, state CircuitState) {
	CircuitBreakerState.WithLabelValues(registry).Set(CircuitStateToFloat(state))
}

// UpdateCatalogImageCount updates the catalog image count by phase
func (m *MetricsRecorder) UpdateCatalogImageCount(namespace, phase string, count float64) {
	CatalogImagesTotal.WithLabelValues(namespace, phase).Set(count)
}

// RecordImageSize records the size of an image
func (m *MetricsRecorder) RecordImageSize(architecture, distro string, sizeBytes float64) {
	ImageSizeBytes.WithLabelValues(architecture, distro).Observe(sizeBytes)
}

// UpdateMultiArchCount updates the multi-architecture image count
func (m *MetricsRecorder) UpdateMultiArchCount(count float64) {
	MultiArchImages.Set(count)
}
