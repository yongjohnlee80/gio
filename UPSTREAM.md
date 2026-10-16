# Keeping the fork current with upstream Gio

This repository is `github.com/yongjohnlee80/gio`, a fork of [Gio](https://gioui.org). On GitHub it
is a fork of `gioui/gio`, which is only a mirror. The real upstream is
[`git.sr.ht/~eliasnaur/gio`](https://git.sr.ht/~eliasnaur/gio), and the sync script fetches from
there. What the fork adds is in [NATIVE_DROP.md](NATIVE_DROP.md).

## Policy

- **Follow upstream; don't diverge.** Upstream fixes the platform layer this fork's consumers depend
  on: Wayland and X11 input methods, the macOS view, GPU recovery. Owning those on three platforms
  costs more than syncing.
- **Sync at upstream release tags** (`v0.10.4`, …), not at every commit.
- **Cherry-pick a single upstream fix** between releases when a consumer needs it now.
- **Never push an upstream tag.** In this repository a `v0.10.x` tag would be a version of
  `github.com/yongjohnlee80/gio` whose `go.mod` names `gioui.org`, and a higher one than ours.
  - The script fetches upstream tags into `refs/upstream-tags/`, never `refs/tags/`.
  - It sets `remote.upstream.tagOpt --no-tags`.
- **Contribute the drop feature upstream** when it has had native use. Patches go to
  [gio-patches](https://lists.sr.ht/~eliasnaur/gio-patches), with `git send-email` and without the
  module rename. If upstream takes it, consumers go back to `gioui.org` and this fork retires.

## Versions

- The fork's tags are its own line, `v0.1.x`, starting at `v0.1.0`: the drops, on upstream
  `c170b58`.
- **Each sync or cherry-pick is the next patch** (`v0.1.1`, …).
- **A new minor or major needs the owner's approval.** That covers a breaking change in what the
  fork adds, or an upstream release that breaks its API.
- **Each tag's note names its upstream base** (a tag, or a commit with the tag it follows).
  `NATIVE_DROP.md`'s first paragraph is updated to the same base.
- **Tag only after the fork's CI passes** on the commit: every `native drop` and `govulncheck` job
  (Linux, macOS and Windows).

## Procedure

1. **Branch from `main`:** `git switch -c sync/v0.10.4 main`.
2. **Merge:** `scripts/sync-upstream.sh merge v0.10.4`. The ref can be `upstream/main` or a commit.
   The script:
   - fetches the ref;
   - merges it without committing;
   - rewrites every `gioui.org/...` import to `github.com/yongjohnlee80/gio/...`, apart from
     `gioui.org/shader`, which stays upstream's module;
   - restores `go.mod`'s module line and runs `gofmt`;
   - runs `check`.
3. **On conflicts** it stops and lists them. Resolve them (see Conflicts), then run
   `scripts/sync-upstream.sh rename && scripts/sync-upstream.sh check`.
4. **Review and commit:** read `git diff --cached`. The script prints the commit command,
   `sync: upstream v0.10.4 (<commit>)`. It never commits or pushes.
5. **Pull request:** open one against `main`, and run the gates.
6. **After merge:** tag the next patch with its upstream base in the note.
7. **Consumers:** move golib/gui to the new tag, and run the IME matrix there (see Gates).

To cherry-pick one fix:
1. `git fetch --no-tags upstream`.
2. `git cherry-pick -x <commit>`, resolving any conflicts.
3. `scripts/sync-upstream.sh rename && scripts/sync-upstream.sh check`, then
   `git commit --amend --no-edit`.

## Conflicts

- **Import blocks.** Any file upstream edited near its imports conflicts on them. Take upstream's
  side; `rename` rewrites the paths again. A new upstream file only needs `rename`.
- **`go.mod`.** Take upstream's requirements, and keep the `module github.com/yongjohnlee80/gio`
  line, which `rename` restores. Then `go mod tidy` and review `go.sum`.
- **The files the drops change.** These need real resolution. Keep upstream's change, then
  re-apply the drop's part:
  - `app/os_wayland.go`:
    - the `wlSeat` drop fields (`drag`, `drops`, `dropActions`) and `wlDrop`/`wlDropRead`;
    - `flushOffers` keeping drag and drop offers;
    - `gio_onDataDeviceEnter`/`Leave`/`Motion`/`Drop` and `acceptDrop`;
    - `gio_onDataOfferAction`;
    - the `clipReads` channel of `event.Event`, and `dispatch`'s `wlDropRead` branch;
    - `window.destroy` and `wlSeat.destroy` releasing offers and readers;
    - `wlDisplay.wakeup`'s `notifyMu`.
  - `app/os_x11.go`:
    - the `drop x11Drop` field and `initDrop` in `newX11Window`;
    - the `receiveDrop` and `dropMessage` hooks in `handleEvents`;
    - the deadline check and the bounded `poll` timeout in the event loop;
    - `finishDrop` in `destroy`.

    The protocol itself is in `os_x11_drop.go`, which upstream doesn't have.
  - `app/os_macos.go`: `gio_onDesktopDrop`.
  - `app/os_macos.m`:
    - `GioView`'s `draggingEntered`, `draggingUpdated`, `prepareForDragOperation` and
      `performDragOperation`;
    - `registerForDraggedTypes` in `gio_createView`.
  - `app/window.go`:
    - `eventSummary.drops`;
    - `nextEvent` delivering drops after a queued frame;
    - `processEvent`'s `DropEvent` case.
- **When unsure,** diff the fork against its last upstream base to see the drop's own hunks:
  `git diff <base> main -- <file>`. The rename makes most of that diff, so read past the import
  lines.

## Gates

- **The fork's CI.**
  - `native drop` runs on Linux, macOS and Windows: the build, `go vet ./app`, and the tests of
    `./app` and `./io/input`, with the race detector on Linux and macOS. On Linux they include the
    Xvfb XDND cells (`-tags gio_xdnd_test`), and the separate `nox11` and `nowayland` builds run
    there too. Windows has no native drops yet, so its job proves the fork still builds and passes
    there.
  - `govulncheck` runs on Linux, macOS and Windows.
- **VM43, for agents.** Run the same tests in `golang:1.25` with the Linux window-system libraries
  and Xvfb. Run them against a `git archive` of the head, never a shared worktree. Keep the mutants
  that guard the drop: the `XdndStatus` flag, the finish order, the deadline, the late-property
  delete, and drops after frames.
- **The consumer's IME contract.** golib/gui's `gio_pin_test.go` requires the exact fork version.
  It fails until `verifiedGio` names a release whose input-method handling a person checked
  natively:
  - Hangul with an input method on Wayland and on macOS;
  - the matrix in the KB's `playbooks/gio-upgrade` (M1–M4);
  - the macOS syllable-boundary replay, `gui/testdata/hangul-macos-boundary.jsonl`.

  A sync that touches `app/` input or `ime` code isn't done until that check is recorded. Headless
  tests don't count as that evidence.
- **Native drops**, when a sync touches the drop files: drag from a file manager on Wayland and on
  X11, a browser link, and a Finder drag, each with a refused case.

## Security

- **Upstream publishes no security advisories,** so the fork watches its own code and modules.
  - `govulncheck` runs on every push and pull request, and weekly, since a new advisory can name a
    module that didn't change.
  - It reports for the platform it runs on: Linux, macOS and Windows, the platforms consumers
    ship on (AutoDoc v0.2 adds Windows).
  - It uses v1.7.0, the newest release that builds with Go 1.25. Move it when the toolchain moves.
- **Dependencies move on their own clock.** Text shaping, image decoding and system calls are
  separate modules: `github.com/go-text/typesetting`, `golang.org/x/image`, `golang.org/x/sys` and
  `golang.org/x/net`.
  - Bump them with `go get <module>@<version>` and `go mod tidy` in a pull request through the same
    gates, then tag a patch.
  - That doesn't wait for an upstream sync.
- **At each sync, read upstream's log** since the last base for fixes in what handles outside
  input:
  - clipboard and drop transfer;
  - the input methods;
  - window-system messages;
  - file and URL handling.

  Note the security-relevant ones in the pull request.
