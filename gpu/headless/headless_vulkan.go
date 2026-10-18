// SPDX-License-Identifier: Unlicense OR MIT

//go:build (linux || freebsd) && !novulkan

package headless

import (
	"unsafe"

	"github.com/yongjohnlee80/gio/gpu"
	"github.com/yongjohnlee80/gio/internal/vk"
)

type vkContext struct {
	physDev  vk.PhysicalDevice
	inst     vk.Instance
	dev      vk.Device
	queueFam int
	dmabuf   bool
}

func init() {
	newContextFallback = newVulkanContext
}

func newVulkanContext() (context, error) {
	inst, err := vk.CreateInstance()
	if err != nil {
		return nil, err
	}
	physDev, qFam, err := vk.ChoosePhysicalDevice(inst, 0)
	if err != nil {
		vk.DestroyInstance(inst)
		return nil, err
	}
	var exts []string
	dmabuf := vk.SupportsDMABufImport(physDev)
	if dmabuf {
		exts = vk.DMABufExtensions
	}
	dev, err := vk.CreateDeviceAndQueue(physDev, qFam, exts...)
	if err != nil {
		vk.DestroyInstance(inst)
		return nil, err
	}
	ctx := &vkContext{
		physDev:  physDev,
		inst:     inst,
		dev:      dev,
		queueFam: qFam,
		dmabuf:   dmabuf,
	}
	return ctx, nil
}

func (c *vkContext) API() gpu.API {
	return gpu.Vulkan{
		PhysDevice:  unsafe.Pointer(c.physDev),
		Device:      unsafe.Pointer(c.dev),
		Format:      int(vk.FORMAT_R8G8B8A8_SRGB),
		QueueFamily: c.queueFam,
		QueueIndex:  0,
		DMABuf:      c.dmabuf,
	}
}

func (c *vkContext) MakeCurrent() error {
	return nil
}

func (c *vkContext) ReleaseCurrent() {
}

func (c *vkContext) Release() {
	vk.DeviceWaitIdle(c.dev)

	vk.DestroyDevice(c.dev)
	vk.DestroyInstance(c.inst)
	*c = vkContext{}
}
