//go:build !wasip1

package plugin_sdk

// Non-WASM build: registrations and host calls are driven by sdktest.

//nolint:unused
func alloc(size uint32) uint32 { return 0 }

//nolint:unused
func dealloc(ptr uint32, size uint32)   {}
func ReadBytes(ptr, size uint32) []byte { return nil }
func WriteResult(data []byte) uint64    { return 0 }

func Log(msg string, level LogLevel) {
	if h := testHostOf(); h != nil && h.Log != nil {
		h.Log(msg, level)
	}
}

func EmitMetric(name string, metricType MetricKind, value float64, labels map[string]string) {
	if h := testHostOf(); h != nil && h.Metric != nil {
		h.Metric(name, metricType, value, labels)
	}
}

func hostCallRawImpl(cmd string, args []byte) ([]byte, error) {
	h := testHostOf()
	if h == nil || h.HostCall == nil {
		return nil, nil
	}
	return h.HostCall(cmd, args)
}
