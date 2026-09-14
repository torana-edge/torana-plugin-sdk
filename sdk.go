//go:build wasip1

package plugin_sdk

import (
	"encoding/json"
	"sync"
	"unsafe"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
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

//go:wasmexport abi_version
func abi_version() uint64 { return ABIVersion }

//go:wasmexport supported_hooks
func supported_hooks() uint32 {
	return uint32(registeredHookBitmap())
}

//go:wasmexport run_hook
func run_hook(ptr, size uint32) uint64 {
	inputBytes := ReadBytes(ptr, size)
	in, err := pbv1.DecodeHookInput(inputBytes)
	if err != nil {
		panic("torana sdk: decode run_hook: " + err.Error())
	}
	outBytes, err := DispatchHook(in)
	if err != nil {
		panic("torana sdk: run_hook: " + err.Error())
	}
	if len(outBytes) == 0 {
		return 0
	}
	return WriteResult(outBytes)
}

//go:wasmimport env log
func hostLog(level int32, ptr uint32, length uint32)

func Log(msg string, level LogLevel) {
	b := []byte(msg)
	if len(b) == 0 {
		return
	}
	ptr := alloc(uint32(len(b)))
	copy(ReadBytes(ptr, uint32(len(b))), b)
	hostLog(int32(level), ptr, uint32(len(b)))
	dealloc(ptr, uint32(len(b)))
}

//go:wasmimport env emit_metric
func hostEmitMetric(metricType int32, ptr uint32, length uint32, value float64, labelsPtr uint32, labelsLen uint32)

func EmitMetric(name string, metricType MetricKind, value float64, labels map[string]string) {
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
	hostEmitMetric(int32(metricType), ptr, uint32(len(b)), value, lPtr, lLen)
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
