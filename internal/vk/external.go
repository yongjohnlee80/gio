// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux || freebsd

package vk

/*
#cgo CFLAGS: -Werror -Werror=return-type

#define VK_NO_PROTOTYPES 1
#define VK_DEFINE_NON_DISPATCHABLE_HANDLE(object) typedef uint64_t object;
#include <vulkan/vulkan.h>
#include <stdlib.h>
#include <string.h>

// The chains below are built in C: Go may not hand C a Go pointer to memory holding Go pointers.

static uint32_t gioInstanceVersion(PFN_vkEnumerateInstanceVersion f) {
	uint32_t v = VK_API_VERSION_1_0;
	if (f != NULL && f(&v) != VK_SUCCESS) {
		v = VK_API_VERSION_1_0;
	}
	return v;
}

static void gioDeviceUUID(PFN_vkGetPhysicalDeviceProperties2 f, VkPhysicalDevice pd, uint8_t *uuid) {
	VkPhysicalDeviceIDProperties id = { .sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_ID_PROPERTIES };
	VkPhysicalDeviceProperties2 props = { .sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_PROPERTIES_2, .pNext = &id };
	f(pd, &props);
	memcpy(uuid, id.deviceUUID, VK_UUID_SIZE);
}

// gioModifiers fills mods and planes (up to max) with the DRM format modifiers fmt has, and the
// features each samples with; it returns how many there are.
static uint32_t gioModifiers(PFN_vkGetPhysicalDeviceFormatProperties2 f, VkPhysicalDevice pd, VkFormat fmt, uint64_t *mods, uint32_t *planes, VkFormatFeatureFlags *features, uint32_t max) {
	VkDrmFormatModifierPropertiesListEXT list = { .sType = VK_STRUCTURE_TYPE_DRM_FORMAT_MODIFIER_PROPERTIES_LIST_EXT };
	VkFormatProperties2 props = { .sType = VK_STRUCTURE_TYPE_FORMAT_PROPERTIES_2, .pNext = &list };
	f(pd, fmt, &props);
	if (list.drmFormatModifierCount == 0) {
		return 0;
	}
	VkDrmFormatModifierPropertiesEXT *p = calloc(list.drmFormatModifierCount, sizeof(*p));
	if (p == NULL) {
		return 0;
	}
	list.pDrmFormatModifierProperties = p;
	f(pd, fmt, &props);
	uint32_t n = list.drmFormatModifierCount < max ? list.drmFormatModifierCount : max;
	for (uint32_t i = 0; i < n; i++) {
		mods[i] = p[i].drmFormatModifier;
		planes[i] = p[i].drmFormatModifierPlaneCount;
		features[i] = p[i].drmFormatModifierTilingFeatures;
	}
	free(p);
	return n;
}

// gioImportable reports whether a sampled 2D image of fmt with modifier mod can be imported from
// a dma-buf, and its maximum extent.
static int gioImportable(PFN_vkGetPhysicalDeviceImageFormatProperties2 f, VkPhysicalDevice pd, VkFormat fmt, uint64_t mod, uint32_t *maxW, uint32_t *maxH) {
	VkPhysicalDeviceImageDrmFormatModifierInfoEXT modInfo = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_IMAGE_DRM_FORMAT_MODIFIER_INFO_EXT,
		.drmFormatModifier = mod,
		.sharingMode = VK_SHARING_MODE_EXCLUSIVE,
	};
	VkPhysicalDeviceExternalImageFormatInfo ext = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_EXTERNAL_IMAGE_FORMAT_INFO,
		.pNext = &modInfo,
		.handleType = VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT,
	};
	VkPhysicalDeviceImageFormatInfo2 info = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_IMAGE_FORMAT_INFO_2,
		.pNext = &ext,
		.format = fmt,
		.type = VK_IMAGE_TYPE_2D,
		.tiling = VK_IMAGE_TILING_DRM_FORMAT_MODIFIER_EXT,
		.usage = VK_IMAGE_USAGE_SAMPLED_BIT,
	};
	VkExternalImageFormatProperties extProps = { .sType = VK_STRUCTURE_TYPE_EXTERNAL_IMAGE_FORMAT_PROPERTIES };
	VkImageFormatProperties2 props = { .sType = VK_STRUCTURE_TYPE_IMAGE_FORMAT_PROPERTIES_2, .pNext = &extProps };
	if (f(pd, &info, &props) != VK_SUCCESS) {
		return 0;
	}
	if ((extProps.externalMemoryProperties.externalMemoryFeatures & VK_EXTERNAL_MEMORY_FEATURE_IMPORTABLE_BIT) == 0) {
		return 0;
	}
	*maxW = props.imageFormatProperties.maxExtent.width;
	*maxH = props.imageFormatProperties.maxExtent.height;
	return 1;
}

// gioImportDmaBuf makes a sampled image over a one-plane dma-buf: explicit modifier layout, a
// dedicated allocation importing fd. On VK_SUCCESS fd belongs to the implementation; on failure
// it is still the caller's.
static VkResult gioImportDmaBuf(PFN_vkCreateImage createImage, PFN_vkDestroyImage destroyImage,
		PFN_vkGetImageMemoryRequirements memReqs, PFN_vkAllocateMemory allocate, PFN_vkFreeMemory freeMem,
		PFN_vkBindImageMemory bind, VkDevice d, VkFormat fmt, uint32_t w, uint32_t h, uint64_t mod,
		uint64_t offset, uint64_t pitch, int fd, uint32_t memType, VkImage *outImg, VkDeviceMemory *outMem, uint32_t *typeBits, int *consumed) {
	*consumed = 0;
	VkSubresourceLayout plane = { .offset = offset, .rowPitch = pitch };
	VkImageDrmFormatModifierExplicitCreateInfoEXT explicit = {
		.sType = VK_STRUCTURE_TYPE_IMAGE_DRM_FORMAT_MODIFIER_EXPLICIT_CREATE_INFO_EXT,
		.drmFormatModifier = mod,
		.drmFormatModifierPlaneCount = 1,
		.pPlaneLayouts = &plane,
	};
	VkExternalMemoryImageCreateInfo extInfo = {
		.sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_IMAGE_CREATE_INFO,
		.pNext = &explicit,
		.handleTypes = VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT,
	};
	VkImageCreateInfo inf = {
		.sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO,
		.pNext = &extInfo,
		.imageType = VK_IMAGE_TYPE_2D,
		.format = fmt,
		.extent = { w, h, 1 },
		.mipLevels = 1,
		.arrayLayers = 1,
		.samples = VK_SAMPLE_COUNT_1_BIT,
		.tiling = VK_IMAGE_TILING_DRM_FORMAT_MODIFIER_EXT,
		.usage = VK_IMAGE_USAGE_SAMPLED_BIT,
		.sharingMode = VK_SHARING_MODE_EXCLUSIVE,
		.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
	};
	VkImage img;
	VkResult r = createImage(d, &inf, NULL, &img);
	if (r != VK_SUCCESS) {
		return r;
	}
	VkMemoryRequirements reqs;
	memReqs(d, img, &reqs);
	*typeBits = reqs.memoryTypeBits;
	if (memType == UINT32_MAX) {
		destroyImage(d, img, NULL);
		return VK_ERROR_FORMAT_NOT_SUPPORTED; // the caller asked only for the memory types
	}
	VkMemoryDedicatedAllocateInfo dedicated = {
		.sType = VK_STRUCTURE_TYPE_MEMORY_DEDICATED_ALLOCATE_INFO,
		.image = img,
	};
	VkImportMemoryFdInfoKHR import = {
		.sType = VK_STRUCTURE_TYPE_IMPORT_MEMORY_FD_INFO_KHR,
		.pNext = &dedicated,
		.handleType = VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT,
		.fd = fd,
	};
	VkMemoryAllocateInfo alloc = {
		.sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
		.pNext = &import,
		.allocationSize = reqs.size,
		.memoryTypeIndex = memType,
	};
	VkDeviceMemory mem;
	r = allocate(d, &alloc, NULL, &mem);
	if (r != VK_SUCCESS) {
		destroyImage(d, img, NULL);
		return r;
	}
	*consumed = 1; // a successful import owns fd, even if what follows fails
	r = bind(d, img, mem, 0);
	if (r != VK_SUCCESS) {
		freeMem(d, mem, NULL);
		destroyImage(d, img, NULL);
		return r;
	}
	*outImg = img;
	*outMem = mem;
	return VK_SUCCESS;
}

static VkResult gioDeviceExtensions(PFN_vkEnumerateDeviceExtensionProperties f, VkPhysicalDevice pd, uint32_t *n, VkExtensionProperties *props) {
	return f(pd, NULL, n, props);
}

static uint32_t gioDeviceAPIVersion(PFN_vkGetPhysicalDeviceProperties f, VkPhysicalDevice pd) {
	VkPhysicalDeviceProperties props;
	f(pd, &props);
	return props.apiVersion;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// The Vulkan 1.1 and extension entry points the dma-buf import needs, looked up once.
var ext11 struct {
	once                                      sync.Once
	vkEnumerateInstanceVersion                C.PFN_vkEnumerateInstanceVersion
	vkGetPhysicalDeviceProperties2            C.PFN_vkGetPhysicalDeviceProperties2
	vkGetPhysicalDeviceFormatProperties2      C.PFN_vkGetPhysicalDeviceFormatProperties2
	vkGetPhysicalDeviceImageFormatProperties2 C.PFN_vkGetPhysicalDeviceImageFormatProperties2
	vkEnumerateDeviceExtensionProperties      C.PFN_vkEnumerateDeviceExtensionProperties
}

// DMABufExtensions are the device extensions a dma-buf import needs, beyond Vulkan 1.1.
var DMABufExtensions = []string{
	"VK_KHR_external_memory_fd",
	"VK_EXT_external_memory_dma_buf",
	"VK_EXT_image_drm_format_modifier",
	"VK_EXT_queue_family_foreign",
}

// loadExt11 looks the 1.1 entry points up in the loader, which exports them as it does 1.0's.
func loadExt11() {
	ext11.once.Do(func() {
		if vkInit() != nil {
			return
		}
		lib := dlopen("libvulkan.so.1")
		if lib == nil {
			return
		}
		ext11.vkEnumerateInstanceVersion = C.PFN_vkEnumerateInstanceVersion(dlsym(lib, "vkEnumerateInstanceVersion"))
		ext11.vkGetPhysicalDeviceProperties2 = C.PFN_vkGetPhysicalDeviceProperties2(dlsym(lib, "vkGetPhysicalDeviceProperties2"))
		ext11.vkGetPhysicalDeviceFormatProperties2 = C.PFN_vkGetPhysicalDeviceFormatProperties2(dlsym(lib, "vkGetPhysicalDeviceFormatProperties2"))
		ext11.vkGetPhysicalDeviceImageFormatProperties2 = C.PFN_vkGetPhysicalDeviceImageFormatProperties2(dlsym(lib, "vkGetPhysicalDeviceImageFormatProperties2"))
		ext11.vkEnumerateDeviceExtensionProperties = C.PFN_vkEnumerateDeviceExtensionProperties(dlsym(lib, "vkEnumerateDeviceExtensionProperties"))
	})
}

// InstanceVersion is the loader's highest Vulkan version (VK_MAKE_API_VERSION form): 1.0 when
// the loader predates vkEnumerateInstanceVersion.
func InstanceVersion() uint32 {
	loadExt11()
	return uint32(C.gioInstanceVersion(ext11.vkEnumerateInstanceVersion))
}

// Version11 is VK_API_VERSION_1_1.
const Version11 = uint32(C.VK_API_VERSION_1_1)

// SupportsDMABufImport reports whether pd can import dma-bufs: Vulkan 1.1 on the device and
// the loader, and every extension in DMABufExtensions.
func SupportsDMABufImport(pd PhysicalDevice) bool {
	loadExt11()
	if ext11.vkEnumerateDeviceExtensionProperties == nil || ext11.vkGetPhysicalDeviceFormatProperties2 == nil ||
		ext11.vkGetPhysicalDeviceImageFormatProperties2 == nil || ext11.vkGetPhysicalDeviceProperties2 == nil {
		return false
	}
	if InstanceVersion() < Version11 || uint32(C.gioDeviceAPIVersion(funcs.vkGetPhysicalDeviceProperties, pd)) < Version11 {
		return false
	}
	var n C.uint32_t
	if C.gioDeviceExtensions(ext11.vkEnumerateDeviceExtensionProperties, pd, &n, nil) != C.VK_SUCCESS || n == 0 {
		return false
	}
	props := make([]C.VkExtensionProperties, n)
	if C.gioDeviceExtensions(ext11.vkEnumerateDeviceExtensionProperties, pd, &n, &props[0]) != C.VK_SUCCESS {
		return false
	}
	have := map[string]bool{}
	for _, p := range props[:n] {
		have[C.GoString(&p.extensionName[0])] = true
	}
	for _, e := range DMABufExtensions {
		if !have[e] {
			return false
		}
	}
	return true
}

// DeviceUUID is pd's VkPhysicalDeviceIDProperties.deviceUUID.
func DeviceUUID(pd PhysicalDevice) [16]byte {
	loadExt11()
	var id [16]byte
	if ext11.vkGetPhysicalDeviceProperties2 != nil {
		C.gioDeviceUUID(ext11.vkGetPhysicalDeviceProperties2, pd, (*C.uint8_t)(unsafe.Pointer(&id[0])))
	}
	return id
}

// Modifier is one DRM format modifier of a format and what it can do.
type Modifier struct {
	Mod      uint64
	Planes   int
	Sampled  bool
	MaxW     int
	MaxH     int
	Importer bool // a sampled 2D image with it imports from a dma-buf
}

// FormatModifiers is fmt's modifiers on pd, each checked for dma-buf import; none on a device
// without the dma-buf extensions, where the modifier queries are invalid and their answers
// meaningless (a software device may answer them anyway).
func FormatModifiers(pd PhysicalDevice, fmt Format) []Modifier {
	if !SupportsDMABufImport(pd) {
		return nil
	}
	const max = 64
	var mods [max]C.uint64_t
	var planes [max]C.uint32_t
	var features [max]C.VkFormatFeatureFlags
	n := int(C.gioModifiers(ext11.vkGetPhysicalDeviceFormatProperties2, pd, fmt, &mods[0], &planes[0], &features[0], max))
	out := make([]Modifier, 0, n)
	for i := range n {
		m := Modifier{Mod: uint64(mods[i]), Planes: int(planes[i]), Sampled: features[i]&C.VK_FORMAT_FEATURE_SAMPLED_IMAGE_BIT != 0}
		var w, h C.uint32_t
		if m.Sampled && C.gioImportable(ext11.vkGetPhysicalDeviceImageFormatProperties2, pd, fmt, mods[i], &w, &h) != 0 {
			m.Importer, m.MaxW, m.MaxH = true, int(w), int(h)
		}
		out = append(out, m)
	}
	return out
}

// ImportDMABuf makes a sampled image over a one-plane dma-buf. fd must be the caller's own
// descriptor (a dup). consumed reports whether the implementation took it: then the caller must
// never close it, whatever the error; otherwise it is still the caller's to close.
func ImportDMABuf(pd PhysicalDevice, d Device, format Format, w, h int, mod, offset, pitch uint64, fd int) (img Image, mem DeviceMemory, consumed bool, err error) {
	var bits C.uint32_t
	var took C.int
	// First learn the image's memory types, then import into the best of them.
	C.gioImportDmaBuf(funcs.vkCreateImage, funcs.vkDestroyImage, funcs.vkGetImageMemoryRequirements, funcs.vkAllocateMemory,
		funcs.vkFreeMemory, funcs.vkBindImageMemory, d, format, C.uint32_t(w), C.uint32_t(h), C.uint64_t(mod),
		C.uint64_t(offset), C.uint64_t(pitch), C.int(fd), C.UINT32_MAX, &img, &mem, &bits, &took)
	if bits == 0 {
		return nilImage, nilDeviceMemory, false, errors.New("vulkan: the dma-buf image has no memory type")
	}
	idx, ok := findMemoryTypeIndex(pd, bits, C.VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT)
	if !ok {
		idx, ok = findMemoryTypeIndex(pd, bits, 0)
	}
	if !ok {
		return nilImage, nilDeviceMemory, false, errors.New("vulkan: no memory type for the dma-buf")
	}
	r := C.gioImportDmaBuf(funcs.vkCreateImage, funcs.vkDestroyImage, funcs.vkGetImageMemoryRequirements, funcs.vkAllocateMemory,
		funcs.vkFreeMemory, funcs.vkBindImageMemory, d, format, C.uint32_t(w), C.uint32_t(h), C.uint64_t(mod),
		C.uint64_t(offset), C.uint64_t(pitch), C.int(fd), C.uint32_t(idx), &img, &mem, &bits, &took)
	if err := vkErr(r); err != nil {
		return nilImage, nilDeviceMemory, took != 0, fmt.Errorf("vulkan: import dma-buf: %w", err)
	}
	return img, mem, true, nil
}

// QueueFamilyForeign is VK_QUEUE_FAMILY_FOREIGN_EXT: the owner of memory shared outside Vulkan.
const QueueFamilyForeign = uint32(C.VK_QUEUE_FAMILY_FOREIGN_EXT)

// BuildAcquireBarrier is the barrier that takes a freshly imported image from its foreign owner:
// GENERAL in the foreign queue family to SHADER_READ_ONLY_OPTIMAL in queueFamily.
func BuildAcquireBarrier(img Image, queueFamily int) ImageMemoryBarrier {
	b := BuildImageMemoryBarrier(img, 0, ACCESS_SHADER_READ_BIT, IMAGE_LAYOUT_GENERAL, IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL, 0, 1)
	b.srcQueueFamilyIndex = C.uint32_t(QueueFamilyForeign)
	b.dstQueueFamilyIndex = C.uint32_t(queueFamily)
	return b
}
