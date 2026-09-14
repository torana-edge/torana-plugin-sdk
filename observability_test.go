//go:build !wasip1

package plugin_sdk

import "testing"

func TestTypedObservabilityUsesCanonicalKinds(t *testing.T) {
	var gotLevel LogLevel
	var gotKind MetricKind
	var gotLabels map[string]string
	WithTestHost(&TestHost{
		Log: func(_ string, level LogLevel) { gotLevel = level },
		Metric: func(_ string, kind MetricKind, _ float64, labels map[string]string) {
			gotKind, gotLabels = kind, labels
		},
	}, func() {
		Info("ready")
		Gauge("queue_depth", 2, map[string]string{"region": "test"})
	})
	if gotLevel != LogLevelInfo {
		t.Fatalf("level = %v, want info", gotLevel)
	}
	if gotKind != MetricGauge || gotLabels["region"] != "test" {
		t.Fatalf("metric = %v labels=%v, want gauge with labels", gotKind, gotLabels)
	}
}
