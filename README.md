# atari-hdd-image-creator

`ahic` creates bootable Atari ST and TT hard disk images from Windows, Linux or macOS.

It partitions an image for a chosen TOS version and installs a free driver using that driver's original boot loaders.

**Status:** images pass the loader simulation below, but none has been booted in Hatari or on real hardware yet. See [the test plan](docs/testing.md).

## Build

```sh
go build ./cmd/ahic         # for this computer
tools/build_release.sh      # for Linux, Windows and macOS, into dist/
```

Windows and macOS builds compile, but have only been run on Linux so far.

## Usage

Run `ahic` with no arguments for an interactive wizard. It can create an image or change the driver on an existing one.

The same actions are available as commands:

```sh
ahic create --size 512 --tos 1.04 --driver ahdi disk.img
ahic create --size 1024 --tos 3.06 --bus scsi --driver icd tt.img
ahic install-driver --driver cbhd disk.img
ahic info disk.img
ahic verify disk.img
ahic drivers
```

`install-driver` replaces the boot loaders and the driver file on an existing image. Partitions and other files stay as they are.

- Unchanged bundled drivers are removed.
- A changed file with the new driver's name is kept as `.OLD`.
- ICD needs `--bus` unless it is already installed, because its root loader depends on the bus.
- Images with a DOS partition table, or with partitions in ICD's extended table, are refused.

| Option | Values |
|---|---|
| `--size` | 4 to 1024 MB, up to 399 MB on TOS 1.02 |
| `--tos` | 1.02, 1.04, 1.06, 1.62, 2.05, 2.06, 3.01, 3.05, 3.06 |
| `--driver` | `ahdi`, `cbhd`, `icd` |
| `--bus` | `acsi` (default), or `scsi` with TOS 3.0x |
| `--force` | Overwrite an existing file |

Images use a 15 MB boot partition, then up to 3 equal data partitions. Images under 32 MB use one partition.

## Development

```sh
go test ./...
python3 tools/simulate_boot.py disk.img SHDRIVER.SYS acsi 1.04
```

- `tools/extract_loaders.py` rebuilds `assets/drivers.zip` from the original driver packages.
- `tools/simulate_boot.py` needs the `unicorn` Python package. It runs the real 68000 loaders from an image against a fake TOS, and stops when the driver starts.
- `docs/loaders.md` explains how each loader works and the rules images must follow.

## Bundled drivers

`assets/drivers.zip` contains third-party software that keeps its original copyright. None of it is covered by this project's MIT License.

| Driver | Version | Status |
|---|---|---|
| AHDI | 6.061 | Atari copyright, licence status unclear |
| CBHD | 4.57 | Freeware, unchanged copies allowed |
| ICD PRO | 6.5.5 | ICD copyright, licence status unclear |

## Licence

The source code is under the MIT License, except for the bundled drivers above. See [LICENSE](LICENSE).
