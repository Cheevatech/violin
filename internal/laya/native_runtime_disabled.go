//go:build !cgo

package laya

import "errors"

func nativeRuntimeAvailable() bool { return false }

func openNativeRuntime(root, variant string, preferCoreML bool) (InferenceRuntime, string, error) {
	return nil, "", errors.New("upstream Laya ONNX inference requires a Go build with CGO enabled")
}
