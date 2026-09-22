# Xiaomi Camera Video Auto-Merge Tool

> A one-stop tool for **auto-merging and smart archiving** of Xiaomi camera recordings on Synology NAS, with scheduled runs, per-channel merging, and duplicate-backup protection.

![Go](https://img.shields.io/badge/Go-1.19%2B-00ADD8?logo=go&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-Multi-arch-2496ED?logo=docker&logoColor=white)
![Synology](https://img.shields.io/badge/Synology-DSM%207.x-00B4EF?logo=synology&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-green)

**English**: This document | **简体中文**: [README.md](README.md)

---

## Table of Contents

- [Background](#background)
- [Features](#features)
- [How It Works](#how-it-works)
- [Project Structure](#project-structure)
- [Quick Start (Docker)](#quick-start-docker)
- [Environment Variables](#environment-variables)
- [Output Structure](#output-structure)
- [Synology NAS Scheduled Tasks](#synology-nas-scheduled-tasks)
- [Manual Run](#manual-run)
- [Duplicate-Proof Mechanism](#duplicate-proof-mechanism)
- [Run Logs](#run-logs)
- [FAQ](#faq)
- [Acknowledgements & Copyright](#acknowledgements--copyright)
- [License](#license)

---

## Background

Xiaomi cameras (or cameras with SD-card / synced storage) split recordings into many small `.mp4` segments of **about 19 minutes each**, continuously written to the NAS shared folder. This leads to three pain points:

| Pain point | Description |
| --- | --- |
| Huge number of files | A single camera produces 150+ segments per day; thousands accumulate in days |
| Hard to replay | Each segment is only 19 minutes; reviewing history requires opening files one by one |
| Messy management | Files are laid out flat under the camera directory, with no timeline-based retrieval |

This project runs a lightweight Go program on the NAS as a **Docker container**, automating the full pipeline of "**grouping & merging → time-based archiving → source cleanup**", and hooks the job into Synology's "Task Scheduler" for **fully unattended operation**.

---

## Features

- ✅ **Per-channel merging**: `XiaomiCamera_01_xxx/0` (main stream) and `/1` (sub stream) are two independent channels — merged and named separately, never mixed. Flat single-stream directories are auto-detected as single-channel.
- ✅ **Smart grouping (≈10 per group)**: segments are merged in chronological order, `GROUP_SIZE=10` by default; if the last group has fewer than `MIN_GROUP_SIZE=8` segments, the tool **waits for the next run** to avoid fragmented tiny files.
- ✅ **Auto-archive by year/month/day**: outputs to `merged/<year>/<month>/<day>/`; filenames are the timeline itself (group start time + channel short name).
- ✅ **Auto-delete sources after merge**: frees disk space, no duplicate storage.
- ✅ **Scheduled auto-run**: configurable via Synology Task Scheduler (example: 00:00 / 12:00 / 20:00 daily).
- ✅ **Overlap-proof & duplicate-proof**: `docker ps` guard at script level + "skip if output exists" at program level + source deletion — three layers prevent duplicate backups.
- ✅ **Lightweight multi-arch image**: two-stage build, final image ~100MB; supports `linux/amd64` and `linux/arm64` (Intel/AMD and ARM Synology).
- ✅ **Well-commented**: source, Dockerfile and compose files come with detailed Chinese comments plus a full deployment & operations manual.

---

## How It Works

```
Xiaomi camera ──sync──▶ NAS /volume1/CCTV
                            │
                            ▼
            ┌─────────────────────────────┐
            │   Docker container (one-shot)│
            │  xiaomi-camera-merge:local    │
            │                               │
            │  1. Scan camera directories   │
            │  2. Detect units (flat/dual)  │
            │  3. Sort by start time, group │
            │     of 10                     │
            │  4. <8 left → wait for more   │
            │  5. ffmpeg lossless merge     │
            │     (-c copy)                 │
            │  6. Output merged/<y>/<m>/<d>/│
            │  7. Delete merged sources     │
            └─────────────────────────────┘
                            │
                            ▼
        Synology "Task Scheduler" launches
        the container (00:00 / 12:00 / 20:00)
```

- **Unit detection**: the scanner auto-distinguishes "flat single-channel" (e.g. `XiaomiCamera_00_xxx`, short name `00`) from "dual-channel" (e.g. `XiaomiCamera_01_xxx/0`, `/1`, short names `01_0` / `01_1`).
- **Merging**: uses `ffmpeg` with `-c copy` for lossless merging (no re-encoding — fast, zero quality loss).
- **Execution model**: the container is a **one-shot task** — it exits after finishing, uses no memory when idle; the scheduler is responsible for periodic launches.

---

## Project Structure

```
xiaomi-camera-merge/
├── main.go                      # Main program (v3.0, fully commented)
├── lib/
│   └── log/
│       └── log.go               # Logging component (internal package)
├── Dockerfile                   # Multi-arch two-stage build
├── docker-compose.yml           # Synology Container Manager deployment
├── go.mod                       # Go module (no third-party deps)
├── .dockerignore                # Docker build context exclusions
├── .gitignore                   # Git ignore rules
├── synology-task-scheduler.sh   # Synology Task Scheduler script
├── README.md                    # This project's Chinese readme
├── README_EN.md                 # This file (English)
├── 部署与操作文档.md             # Full deployment & ops manual (Chinese)
└── 部署与操作文档.docx           # Manual (Word)
```

---

## Quick Start (Docker)

### Option 1: docker compose (recommended, full config)

```bash
git clone https://github.com/1272095268/xiaomi-camera-merge.git
cd xiaomi-camera-merge

# Build the image and run once (one-shot container, exits after finishing)
docker compose up --build
```

### Option 2: Manual docker build / run

```bash
docker build -t xiaomi-camera-merge:local .

docker run --rm --name xiaomi-camera-merge \
  -e TZ=Asia/Shanghai \
  -e DELETE_SUCCESS=true \
  -e MAX_MERGE=0 \
  -e VIDEO_EXT=mp4 \
  -e MIN_AGE_MIN=30 \
  -e SKIP_CURRENT=true \
  -e GROUP_SIZE=10 \
  -e MIN_GROUP_SIZE=8 \
  -v /volume1/CCTV:/app/video \
  xiaomi-camera-merge:local
```

> Replace `/volume1/CCTV` with your actual recording directory.

### Option 3: Synology Container Manager (GUI)

1. Open **Container Manager → Project → Add**;
2. Choose "Create from path", point to the project directory (the one containing `docker-compose.yml`);
3. Confirm and click **Build**; the container runs the merge task automatically once built.

---

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `TZ` | `Asia/Shanghai` | Timezone; keeps "current hour" logic and archive paths correct |
| `DELETE_SUCCESS` | `true` | Delete merged source segments after success (frees space) |
| `MAX_MERGE` | `0` | Max groups to merge per run; `0` = unlimited |
| `VIDEO_EXT` | `mp4` | Extension of segments to merge |
| `MIN_AGE_MIN` | `30` | Skip files modified within the last 30 minutes (in-progress writes) |
| `SKIP_CURRENT` | `true` | Skip segments starting in the current hour (in-progress writes) |
| `GROUP_SIZE` | `10` | Segments per group (v3.0, "about 10 per merge") |
| `MIN_GROUP_SIZE` | `8` | Last group smaller than this waits for the next run |

---

## Output Structure

```
/volume1/CCTV/merged/
├── 2026/
│   ├── 09/
│   │   ├── 01/
│   │   │   ├── 20260901191847_00.mp4      # flat single-channel (short name 00)
│   │   │   └── 20260901203034_00.mp4
│   │   └── 22/
│   │       └── 20260922095750_01_1.mp4    # dual-channel sub stream (01_1)
```

- Directory: `merged/<year>/<month>/<day>/`
- Filename: `<group_start_time_14digits>_<channel_short_name>.mp4`
  - Flat single-channel: `00`
  - Dual-channel: `01_0` (main) / `01_1` (sub)
- Sorted by name = timeline; browsing history by day is intuitive.

---

## Synology NAS Scheduled Tasks

The program itself is a **one-shot task**; scheduled auto-runs rely on Synology "Task Scheduler":

1. Open **Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script**;
2. General: Task name `xiaomi-camera-merge-task`, user `root`;
3. Schedule: set daily run times (example 00:00 / 12:00 / 20:00);
4. Task settings: paste the contents of [`synology-task-scheduler.sh`](synology-task-scheduler.sh) and save.

The script includes **overlap protection**: if a merge container is already running (e.g. a large backlog), the scheduled run skips this round to avoid two processes touching the same files.

---

## Manual Run

Run a merge immediately at any time:

- **Synology Task Scheduler**: select the task → click the top **Run** button;
- or run `docker run` from the command line (see [Quick Start](#quick-start-docker)).

---

## Duplicate-Proof Mechanism

| Layer | Mechanism | Effect |
| --- | --- | --- |
| Script | `docker ps` guard skips if a container is running | Prevents overlapping runs |
| Program | Skips a group if its output file already exists | Prevents duplicate merging |
| Data | Deletes sources after successful merge | Sources gone — no duplicate backup possible |

With all three layers, even if multiple scheduled tasks fire at once, no duplicate output is produced.

---

## Run Logs

Each run writes a log under `CCTV/xiaomi-video-merge-log/`:

```
20260922_151303.log   # Naming: YYYYMMDD_HHMMSS.log
```

Logs record: config echo, unit detection, grouping details, skip reasons (insufficient group), merge results, deleted-source counts, and totals (success/failed/skipped groups) for troubleshooting and auditing.

---

## FAQ

**Q1: Why isn't a final group of only 5 files merged?**
By design: if the last group has fewer than `MIN_GROUP_SIZE` (default 8), the tool skips it and waits for the next run to accumulate enough segments, avoiding fragmented tiny files.

**Q2: Are source files deleted after merging?**
Yes. With `DELETE_SUCCESS=true`, merged source segments are deleted to free space. Set it to `false` if you want to keep them.

**Q3: Will dual channels get mixed together?**
No. `0` and `1` under `XiaomiCamera_01_xxx` are independent units — sorted, merged and named separately (`01_0` / `01_1`).

**Q4: Why does the container exit after running?**
By design: it is a one-shot task container. It exits after finishing and does not occupy memory when idle; scheduled launches are handled by the Task Scheduler. This is the most resource-efficient approach.

**Q5: Does it support ARM Synology (e.g. DS220+)?**
Yes. The Dockerfile uses multi-stage builds and supports both `linux/arm64` and `linux/amd64`.

**Q6: Can it run every hour / every 30 minutes?**
Yes. Just adjust the schedule in Synology Task Scheduler; the program is idempotent, so extra runs never produce duplicate output.

---

## Acknowledgements & Copyright

> **This project is a forked/enhanced version built on top of the original author's open-source project. Special thanks to the original author!**

- **Original project**: [xiaomi-camera-merge](https://github.com/hslr-s/xiaomi-camera-merge)
- **Original author**: [hslr-s (红烧猎人)](https://github.com/hslr-s)
- **Original repository**: <https://github.com/hslr-s/xiaomi-camera-merge>
- **Original version**: v1.1 (includes a Windows batch script and a cross-platform Golang version)

All functionality in this project is based on the original project's ideas and code. Key improvements and additions include:

1. **Per-channel merging**: supports merging and naming the `0`/`1` dual streams of the same camera separately;
2. **Smart grouping**: merges every 10 segments in chronological order (`GROUP_SIZE`), waiting to accumulate when below the lower bound (`MIN_GROUP_SIZE`);
3. **Year/month/day auto-archiving**: outputs `merged/<year>/<month>/<day>/`, filenames as timeline;
4. **Scheduled auto-run**: adapted for Synology Task Scheduler — runs at 00:00 / 12:00 / 20:00 daily, with overlap & duplicate protection;
5. **Lightweight multi-arch image**: two-stage build supporting amd64 / arm64.

**Copyright & attribution notes**:

- When using this project, please **keep the original project's source attribution and the original author's credit**;
- If the original project has specific open-source license requirements, please comply with them as well;
- This repository is licensed under the [MIT License](LICENSE). Free to use and modify — and giving the original project a ⭐ would be much appreciated!

**Once again, thank you to hslr-s (红烧猎人) for the open-source sharing!** 🙏

---

## License

[MIT](LICENSE) © 2026 1272095268

*This project is a derivative of [hslr-s/xiaomi-camera-merge](https://github.com/hslr-s/xiaomi-camera-merge), retaining the original author's attribution and source credit.*
