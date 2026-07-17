//go:build cgo

package sysinfo

// Tests for the CGO-enabled NVML enrichment path. There is deliberately no
// test for the NVML-success path — it requires real NVIDIA hardware and a
// reachable driver, which isn't a unit test; the injected nvmlInit stub keeps
// this one hardware-independent.

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

func TestEnrichNVIDIAWithNVML_Unavailable(t *testing.T) {
	orig := nvmlInit
	t.Cleanup(func() { nvmlInit = orig })
	nvmlInit = func() nvml.Return { return nvml.ERROR_DRIVER_NOT_LOADED }

	input := []GPURaw{{Name: "GeForce RTX 4070", UtilPercent: -1}}
	result := enrichNVIDIAWithNVML(input)
	if len(result) != 1 || result[0].UtilPercent != -1 {
		t.Errorf("expected graceful passthrough when NVML unavailable, got %+v", result)
	}
}
