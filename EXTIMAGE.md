# External images: frames from outside Go's image types

`op/extimage` draws pictures that Go's `image` types don't hold, without a CPU copy where the
device can avoid one:

- frames a video decoder made (VA-API on Linux, VideoToolbox on macOS);
- frames another GPU device rendered (a 3D scene on its own `VkDevice` or `MTLDevice`);
- pixels an application edits in place (a paint canvas).

It is the fork's hardware contract, defined before its drivers. The design and its reasons are
in golib's ADR 1792306654 (golib/gui v0.2.0).

## The contract

- **`Frame`**: one picture, from `NewFrame(Desc, release)`.
  - `Desc` says where its pixels are: `KindMemory` (Go bytes), `KindDMABuf` (Linux file
    descriptors with a DRM format modifier), or `KindIOSurface` (macOS).
  - Formats are `RGBA8`, `BGRA8` and `NV12`, with `Color` (matrix BT.709/601/2020, limited or
    full range) for YUV.
  - The format list is open: a new one is additive, and it is refused until a driver proves it.
- **`Caps`**: what a window draws (`app.Window.ExternalCaps`), as tuples of kind, format,
  modifier and path (`PathZeroCopy` or `PathCPU`), plus the GPU's identity (`DeviceID`) and a
  generation (`Gen`).
  - A frame matching no tuple is refused and reported (`ErrUnsupported`). It is never handed to
    a driver that cannot take it.
  - Where both paths list a tuple, zero-copy wins.
- **`ImageOp`**: `extimage.NewImageOp(frame, report)` sets the brush like `paint.ImageOp`.
  `report` hears each import's outcome.
- **Revisions**: frames of one image share a `Source` (`NewSource`), with `Rev`, `Base` and
  `Dirty`.
  - A texture whose revision is exactly `Base` is updated where `Dirty` says. Anything else
    uploads the whole frame.
  - `Dirty` only ever saves work; it never decides what is drawn.

## Ownership

- A frame is immutable and carries one reference count.
  - Each holder takes a reference and drops it: the application's stream, the renderer while it
    imports, and each texture-cache entry.
  - The producer's `release` runs exactly once, after the last holder, outside every lock.
- A texture replaced while in use is released at the end of the next frame. The device has
  waited for the previous frame by then (Vulkan in `RenderTarget`, Metal in `BeginFrame`).
- **A producer's file descriptors and surfaces are borrowed.** The window never closes or
  releases them.
  - The Vulkan import uses a fresh duplicate per import. A successful import consumes it, and
    only an unconsumed one is closed.
  - The CPU driver maps the borrowed descriptor and needs no duplicate.
  - Metal's texture retains the IOSurface for as long as it lives.

## Drivers

All drivers implement `gpu/internal/driver.ExternalImporter`. Porting to another API means
adding one more implementation in that backend's package.

| Driver | Package | Takes |
|---|---|---|
| Vulkan (Linux) | `gpu/internal/vulkan` | one-plane dma-bufs (RGBA8, BGRA8), for each modifier the device reports as an importable sampled image (`VK_EXT_image_drm_format_modifier` and friends, Vulkan 1.1); acquired from `VK_QUEUE_FAMILY_FOREIGN_EXT` |
| Metal (macOS) | `gpu/internal/metal` | one-plane IOSurfaces (BGRA8, RGBA8), as textures over the surface's own memory |
| CPU (any device) | `gpu/internal/cpuimport` | memory frames, LINEAR dma-bufs (Linux, mmap with the dma-buf sync ioctl) and IOSurfaces (macOS, locked read-only), all formats; NV12 converted in Go; textures pooled by size |

- NV12 is not zero-copy yet. Its planes, and a tiled modifier's memory planes, need a
  conversion pass proven on hardware first.
- Until then NV12 takes the CPU driver: LINEAR dma-bufs, IOSurfaces and memory frames.
- OpenGL and Direct3D 11 have no importer, so they draw every frame through the CPU driver.

## Choosing Vulkan on Linux

Gio prefers EGL on Wayland and leaves Vulkan off on X11 (`vulkanBuggy`). `app.PreferVulkan()`
makes a Wayland window try Vulkan first, falling back to EGL as before. Windows that don't ask
are unchanged.

## Verification

- CI (`.github/workflows/extimage.yml`):
  - the contract, the CPU driver and the renderer's routing;
  - real draws through headless windows: GL and Vulkan via Mesa on Linux, and Metal on macOS,
    where an IOSurface frame is drawn.
- Zero-copy dma-buf import needs hardware with the modifier extension, which Mesa's software
  Vulkan lacks. It is checked by hand on a Linux GPU.

## Upstreaming

- The contract is additive. The changes to upstream code are:
  - one op type (`internal/ops`);
  - a decode case, a size helper and the import routing in `gpu`;
  - the texture-cache `replace` and retirement;
  - `GPU.ExternalCaps`;
  - `app.PreferVulkan`, the Wayland context order and `Window.ExternalCaps`;
  - Vulkan 1.1 instances when the loader has them.
- Each conflicts little on a sync.
- It is worth offering to [gio-patches](https://lists.sr.ht/~eliasnaur/gio-patches) after a
  release's native use, as the drops will be.
