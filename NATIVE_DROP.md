# Native desktop drops

This fork is imported as `github.com/yongjohnlee80/gio`. It is based on upstream commit
`c170b581d523dd6e94dd6e0309a7bb8ae309cc1f`, which includes Gio v0.10.3 plus six upstream
changes. The module/import migration keeps the dependency usable without application-side
`replace` directives. `gioui.org/shader` is a separate dependency and retains its own path.
Syncing with upstream, the fork's versions and its security watch are in
[UPSTREAM.md](UPSTREAM.md).

`app.Window.Event()` delivers `app.DropEvent` for external desktop drops. `Type` is
`text/uri-list` or a supported plain-text MIME type, `Data` is an owned payload, and `Position`
is in client pixels. The event does not change keyboard focus. Applications decide whether
to paste paths, open files, or refuse a payload. File URI decoding and shell quoting belong
in the application/toolkit, not in the platform driver. This implementation only negotiates
copy operations; it never asks a source to delete files.

- **Wayland:** keep the drag offer separate from the clipboard; negotiate URI-list/plain text
  and copy; read the offered pipe asynchronously with a 16 MiB limit and five-second deadline;
  finish on the display loop after a successful read. Leave, window close, and seat destruction
  release offers and cancel pending readers.
- **X11:** advertise XDND v5; handle enter/position/leave/drop, selection conversion, status and
  finished. Refuse unsupported types and oversized or INCR selections rather than pasting a
  partial payload. A pending selection times out after five seconds on the window event loop.
- **macOS:** register the view for file URLs and strings; collect file URLs in a URI list and
  report plain text otherwise. Cocoa's native copy negotiation is used.
- Other platforms keep their existing behavior and do not generate native drop events.

Refusals/timeouts are silent: a failed or unsupported transfer delivers no event. On macOS,
the pasteboard string is materialized before the payload limit is checked; the limit refuses
delivery but does not bound Cocoa's allocation.

The event queue test checks ownership and delivery order. Opt-in `gio_xdnd_test` cells run an
independent Xlib source through the actual X server and XDND handlers under Xvfb: inline and
property-listed URI offers, unsupported types, INCR refusal, and the event loop's timeout.
Desktop CI builds Linux and macOS,
tests the app/input packages, and builds Wayland and X11 separately. Those checks are not proof
of an actual OS drag gesture: compositor/source interoperability and a Finder drag still need
native manual evidence before declaring end-to-end support verified.
