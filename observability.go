package plugin_sdk

// LogLevel is the stable severity understood by the ABI host.
type LogLevel int32

const (
	LogLevelDebug LogLevel = 0
	LogLevelInfo  LogLevel = 1
)

// MetricKind identifies the ABI metric aggregation requested from the host.
type MetricKind int32

const (
	MetricCounter   MetricKind = 0
	MetricHistogram MetricKind = 1
	MetricGauge     MetricKind = 2
)

// Debug and Info are best-effort diagnostics. The void host import cannot
// acknowledge delivery or report a missing grant to the plugin.
func Debug(msg string) { Log(msg, LogLevelDebug) }
func Info(msg string)  { Log(msg, LogLevelInfo) }

func Counter(name string, value float64, labels map[string]string) {
	EmitMetric(name, MetricCounter, value, labels)
}
func Histogram(name string, value float64, labels map[string]string) {
	EmitMetric(name, MetricHistogram, value, labels)
}
func Gauge(name string, value float64, labels map[string]string) {
	EmitMetric(name, MetricGauge, value, labels)
}
