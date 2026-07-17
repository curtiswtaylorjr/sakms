//go:build !cgo

package sysinfo

// enrichNVIDIAWithNVML is a no-op stub for CGO-disabled builds (e.g. the
// Docker image). go-nvml's type definitions live in CGO bridge files, so the
// package cannot be compiled with CGO_ENABLED=0; see gpu_nvml.go for the real
// implementation used in CGO-enabled builds.
func enrichNVIDIAWithNVML(gpus []GPURaw) []GPURaw {
	return gpus
}
