<div align="center">

# Arclights

**A lightweight, open-source alternative to GRYPHLINE's GRYPHLINK launcher for _Arknights: Endfield_.**

Built with [Wails v3](https://v3.wails.io/), [Svelte 5](https://svelte.dev/), and [Go](https://go.dev/).

[![Latest Release](https://img.shields.io/github/v/release/corruptedkey/Arclights?style=flat-square&label=release)](https://github.com/corruptedkey/Arclights/releases/latest)
[![Nightly Build](https://img.shields.io/github/release-date-pre/corruptedkey/Arclights?style=flat-square&label=nightly)](https://github.com/corruptedkey/Arclights/releases/tag/nightly)
[![Platform](https://img.shields.io/badge/platform-Windows-0078D6?style=flat-square&logo=windows)](https://github.com/corruptedkey/Arclights/releases)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![Svelte](https://img.shields.io/badge/Svelte-5-FF3E00?style=flat-square&logo=svelte&logoColor=white)](https://svelte.dev/)

<br />

<img width="1280" height="760" alt="Arclights Launcher Screenshot" src="https://github.com/user-attachments/assets/4681c14f-5629-4f36-946b-6c74984f5227" />

</div>

---

## Features

- **Concurrent Streaming Installer** — Downloads game files directly from the official CDN manifest with multi-connection streaming, pause/resume checkpointing, live speed/ETA tracking, and configurable download speed limits (`100–20,480 KB/s`).
- **Streaming Delta Updates** — Applies patch archives on the fly without writing full zip archives to disk.
- **Direct Game Launch & Rendering Controls** — Launch *Arknights: Endfield* with **Vulkan** or **DirectX 11 (`-dx11`)**, pass custom launch arguments, and toggle troubleshooting mode.
- **Quick Launch (`--start`)** — Launch the game directly without opening the launcher window, ideal for desktop shortcuts or game managers like Steam/Playnite.
- **Dynamic Media, News & Socials** — Displays official banners, announcements, social links, and animated video/static backgrounds with a built-in media switcher.
- **System Tray Integration** — Frameless UI that hides cleanly to the Windows system tray while the game is running and restores automatically when you exit. Supports configurable close behavior and optional Windows startup launch.
- **Game Management** — Select custom install paths, check free disk space before installing, open game/log directories, clear download cache, create desktop shortcuts, and cleanly uninstall game files.

---

## Installation

> **Platform Notice:** Arclights currently targets **Windows 10/11** (64-bit) with Microsoft Edge WebView2 Runtime (pre-installed on modern Windows). Linux and macOS support is planned.

1. Download the latest `arclights.exe` from the [GitHub Releases](https://github.com/corruptedkey/Arclights/releases) page:
   - **Stable:** [Latest Release](https://github.com/corruptedkey/Arclights/releases/latest)
   - **Nightly:** [Rolling Nightly Build](https://github.com/corruptedkey/Arclights/releases/tag/nightly) *(automated builds from `dev`)*
2. Place `arclights.exe` in a folder of your choice and run it.
3. Point Arclights to an existing *Arknights: Endfield* install directory via **Game Settings → Manage Games**, or start a fresh installation directly from the launcher.

---

## CLI Usage

Launch *Arknights: Endfield* immediately without opening the launcher interface:

```powershell
arclights.exe --start
```

`-start` is also accepted as an alias. Desktop shortcuts created by Arclights automatically use `--start`.

---

## Configuration & Data Paths

| Purpose | Path |
| --- | --- |
| Launcher settings | `%LOCALAPPDATA%\arclights\settings.json` |
| WebView2 profile | `%LOCALAPPDATA%\arclights\WebView2\` |
| Logs | `%LOCALAPPDATA%\arclights\logs\` |
| Runtime files | `%LOCALAPPDATA%\arclights\runtime\` |

---

## Building from Source

### Prerequisites

- **Go** `1.24+` (module targets `1.26.0`)
- **Node.js** `20+` and **[Bun](https://bun.sh/)**
- **Wails v3 CLI** (`v3.0.0`):
  ```powershell
  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
  ```
- **[Task](https://taskfile.dev/)** *(optional, for `Taskfile.yml` shortcuts)*

### Development Mode

```powershell
git clone https://github.com/corruptedkey/Arclights.git
cd Arclights

# Install frontend dependencies
cd frontend && bun install && cd ..

# Run in development mode
wails3 dev -config
# or, with Task:
task dev
```

### Production Build

```powershell
wails3 build
```

The compiled executable is output to `bin/arclights.exe`.

### Running Tests & Type Checks

```powershell
# Go unit tests
go test ./...

# Svelte & TypeScript check
cd frontend && bun run check
```

---

## Project Structure

```text
.
├── main.go                 # App entry point, --start quick-launch, tray, and updater setup
├── launcherservice.go      # Wails-bound backend service (install, launch, settings, updates)
├── installer/              # Streaming extractor, manifest decryptor, verifier, and repairer
├── networks/               # GRYPHLINE API client, media bundle, and local config detection
├── frontend/               # Svelte 5 + TypeScript + Vite UI and generated Wails v3 bindings
├── keys/                   # Embedded Ed25519 public keys for stable and nightly auto-updates
├── Taskfile.yml            # Build, dev, and updater signing tasks
└── .github/workflows/      # Automated release and nightly CI builds
```

---

## Roadmap

- [x] README & documentation
- [x] Banners, announcements, dynamic backgrounds & socials
- [x] Game launching, rendering mode selection (Vulkan / DirectX 11) & custom launch arguments
- [x] System tray & auto-launch at startup
- [x] Quick Launch (`arclights --start`) & desktop shortcuts
- [x] Game file verification & automatic repair
- [x] Signed auto-updates (Stable & Nightly channels)
- [~] Game downloads & delta updates *(full downloads verified; live delta updates pending further testing)*
- [ ] Unix support (Linux / macOS)
- [ ] Mod support

---

## Disclaimer

Arclights is an independent, unofficial open-source project and is **not** affiliated with, endorsed by, or associated with GRYPHLINE or HYPERGRYPH. *Arknights: Endfield* and all related assets, trademarks, and services belong to their respective owners.
