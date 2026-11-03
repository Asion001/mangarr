# onnxruntime-purego (vendored)

ONNX Runtime bindings without cgo, from
github.com/shota3506/onnxruntime-purego at 8db8bd7424b2 (MIT, see
LICENSE), used by internal/moodscore. Only the runtime package is kept.

Changed for mangarr: the library is opened through `openLibrary`
(open_unix.go, open_windows.go), so Windows loads onnxruntime.dll with
LoadLibrary instead of purego's Dlopen, which Windows lacks.
