// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux

package vk

import "testing"

// The dma-buf queries run against a real device and agree with each other: a device that lacks
// the extensions is not offered import, and a device that has them lists its modifiers.
func TestDMABufQueriesRunAndAgree(t *testing.T) {
	inst, err := CreateInstance()
	if err != nil {
		t.Skipf("no Vulkan: %v", err)
	}
	defer DestroyInstance(inst)
	pd, _, err := ChoosePhysicalDevice(inst, 0)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	supported := SupportsDMABufImport(pd)
	t.Logf("instance version %#x; dma-buf import supported: %v", InstanceVersion(), supported)
	if id := DeviceUUID(pd); InstanceVersion() >= Version11 && id == ([16]byte{}) {
		t.Error("a Vulkan 1.1 device reported a zero UUID")
	}
	mods := FormatModifiers(pd, FORMAT_R8G8B8A8_SRGB)
	if !supported && len(mods) > 0 {
		t.Errorf("%d modifiers listed on a device without the dma-buf extensions", len(mods))
	}
	if supported {
		t.Logf("RGBA8 modifiers: %+v", mods)
	}
}
