//go:build wasip1

package plugin_sdk

import (
	"context"
	"encoding/json"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

var (
	pinned   = make(map[uint32][]byte)
	pinMutex sync.Mutex
)

//go:wasmexport alloc
func alloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	pinMutex.Lock()
	pinned[ptr] = buf
	pinMutex.Unlock()
	return ptr
}

//go:wasmexport dealloc
func dealloc(ptr uint32, size uint32) {
	pinMutex.Lock()
	delete(pinned, ptr)
	pinMutex.Unlock()
}

// ReadBytes reads from a pointer returned by alloc.
func ReadBytes(ptr, size uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(size))
}

// WriteResult allocates memory, copies data, returns packed ptr|len.
func WriteResult(data []byte) uint64 {
	p := alloc(uint32(len(data)))
	copy(ReadBytes(p, uint32(len(data))), data)
	return uint64(p)<<32 | uint64(len(data))
}

//go:wasmexport supported_hooks
func supported_hooks() uint32 {
	return uint32(registeredHookBitmap())
}

//go:wasmexport run_hook
func run_hook(ptr, size uint32) uint64 {
	inputBytes := ReadBytes(ptr, size)
	var in pbv2.HookInput
	if err := proto.Unmarshal(inputBytes, &in); err != nil {
		panic("torana sdk: decode run_hook: " + err.Error())
	}
	if err := in.Validate(); err != nil {
		panic("torana sdk: invalid hook input: " + err.Error())
	}

	hook := in.HookOf()
	ctx := withRequestID(context.Background(), in.RequestId)

	var (
		hr  *pbv2.HookResult
		err error
	)
	switch hook {
	case pbv2.Hook_HOOK_BEFORE_REQUEST:
		if beforeRequestHandler == nil {
			return 0
		}
		res, herr := beforeRequestHandler(ctx, in.GetChatRequest())
		if herr != nil {
			panic("torana plugin: " + hook.String() + ": " + herr.Error())
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_AFTER_RESPONSE:
		if afterResponseHandler == nil {
			return 0
		}
		ar := in.GetAfterResponse()
		res, herr := afterResponseHandler(ctx, ar.GetResponse(), ar.GetMutable())
		if herr != nil {
			panic("torana plugin: " + hook.String() + ": " + herr.Error())
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_STREAM_CHUNK:
		if streamChunkHandler == nil {
			return 0
		}
		res, herr := streamChunkHandler(ctx, in.GetStreamEvent())
		if herr != nil {
			panic("torana plugin: " + hook.String() + ": " + herr.Error())
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_HTTP_REQUEST:
		if httpRequestHandler == nil {
			return 0
		}
		res, herr := httpRequestHandler(ctx, in.GetHttpRequest())
		if herr != nil {
			panic("torana plugin: " + hook.String() + ": " + herr.Error())
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_TICK:
		if tickHandler == nil {
			return 0
		}
		res, herr := tickHandler(ctx, in.GetTickRequest())
		if herr != nil {
			panic("torana plugin: " + hook.String() + ": " + herr.Error())
		}
		hr, err = res.hookResult()
	default:
		panic("torana sdk: unhandled hook " + hook.String())
	}
	if err != nil {
		panic("torana plugin: " + hook.String() + ": " + err.Error())
	}
	if hr == nil {
		return 0
	}
	if err := hr.ValidateFor(hook); err != nil {
		panic("torana sdk: invalid hook result: " + err.Error())
	}
	outBytes, err := proto.Marshal(hr)
	if err != nil {
		panic("torana sdk: encode run_hook: " + err.Error())
	}
	if len(outBytes) == 0 {
		return 0
	}
	return WriteResult(outBytes)
}

//go:wasmimport env log
func hostLog(level int32, ptr uint32, length uint32)

const (
	LogLevelDebug = 0
	LogLevelInfo  = 1
)

func Log(msg string, level int32) {
	b := []byte(msg)
	if len(b) == 0 {
		return
	}
	ptr := alloc(uint32(len(b)))
	copy(ReadBytes(ptr, uint32(len(b))), b)
	hostLog(level, ptr, uint32(len(b)))
	dealloc(ptr, uint32(len(b)))
}

//go:wasmimport env emit_metric
func hostEmitMetric(metricType int32, ptr uint32, length uint32, value float64, labelsPtr uint32, labelsLen uint32)

const (
	MetricCounter   = 0
	MetricHistogram = 1
	MetricGauge     = 2
)

func EmitMetric(name string, metricType int32, value float64, labels map[string]string) {
	b := []byte(name)
	if len(b) == 0 {
		return
	}
	ptr := alloc(uint32(len(b)))
	copy(ReadBytes(ptr, uint32(len(b))), b)
	defer dealloc(ptr, uint32(len(b)))

	var lPtr, lLen uint32
	if len(labels) > 0 {
		if lb, err := json.Marshal(labels); err == nil {
			lPtr = alloc(uint32(len(lb)))
			copy(ReadBytes(lPtr, uint32(len(lb))), lb)
			lLen = uint32(len(lb))
			defer dealloc(lPtr, lLen)
		}
	}
	hostEmitMetric(metricType, ptr, uint32(len(b)), value, lPtr, lLen)
}

//go:wasmimport env host_call
func hostCallImport(cmdPtr uint32, cmdLen uint32, argsPtr uint32, argsLen uint32) uint64

func hostCallRawImpl(cmd string, args []byte) ([]byte, error) {
	cb := []byte(cmd)
	if len(cb) == 0 {
		return nil, nil
	}
	cPtr := alloc(uint32(len(cb)))
	copy(ReadBytes(cPtr, uint32(len(cb))), cb)
	defer dealloc(cPtr, uint32(len(cb)))

	var aPtr uint32
	if len(args) > 0 {
		aPtr = alloc(uint32(len(args)))
		copy(ReadBytes(aPtr, uint32(len(args))), args)
		defer dealloc(aPtr, uint32(len(args)))
	}

	ret := hostCallImport(cPtr, uint32(len(cb)), aPtr, uint32(len(args)))
	if ret == 0 {
		return nil, nil
	}
	outPtr := uint32(ret >> 32)
	outLen := uint32(ret & 0xFFFFFFFF)
	res := append([]byte(nil), ReadBytes(outPtr, outLen)...)
	dealloc(outPtr, outLen)
	return res, nil
}

// PluginConfig returns this plugin's config JSON blob, or "{}" when unset /
// denied. Uses the transitional string host-call path until env.plugin_config
// returns HostCallResult on Migration B.
func PluginConfig() string {
	res, err := hostCallString("env.plugin_config", "")
	if err != nil || res == "" || isPermissionDenied(res) {
		return "{}"
	}
	return res
}
