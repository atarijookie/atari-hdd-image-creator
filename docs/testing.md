# Test plan

The loader simulation stops as soon as a driver starts, so these tests cover what it can't.

## Creating test images

`tools/make_test_images.sh` creates every combination below in `test-images/`, which git ignores. It checks each image with `ahic verify`.

## Running in Hatari

```sh
hatari --machine st --tos TOS_ROM --acsi 0=ahdi-102.img
hatari --machine tt --tos TOS_ROM --acsi 0=disk.img
hatari --machine tt --tos TOS_ROM --scsi 0=ahdi-tt.img
```

Hatari's manual calls its SCSI support incomplete, so a SCSI failure there still needs checking on the real TT. Available ROMs are 1.04, 3.05 and 4.04; TOS 1.02, 2.06 and 3.06 would need adding.

## What counts as a pass

1. The driver's banner appears during boot.
2. C: and every data partition appear as drives.
3. A file can be copied onto each drive and read back after a reboot.
4. `ahic verify` still passes afterwards.

## Results

AHDI results before the delay fix showed the same boot, with a pause.

Fill in each cell with the date and the result.

| TOS, machine, bus | AHDI | CBHD | ICD |
|---|---|---|---|
| 1.02, ST, ACSI | Boots | Boots | Boots |
| 1.04, ST, ACSI | Boots | Boots | Boots |
| 2.06, ST, ACSI | Boots | Boots | Boots |
| 3.0x, real TT, ACSI | Driver starts, then `Not responding`, no drives | Boots, files survive restart | Boots, files survive restart |
| 3.0x, Hatari TT, ACSI | 4 bombs | 4 bombs | 4 bombs |
| 3.0x, TT, SCSI | Boots | Boots | Freezes after its title |

## Layouts to test

| Layout | Command | Why | Tested on | Result |
|---|---|---|---|---|
| Smallest | `--size 4` | Few clusters on FAT16 | TOS 2.06, ACSI | All three drivers OK |
| Small BGM | `--size 40` | BGM with 512-byte sectors | TOS 2.06, ACSI | All three drivers OK |
| TOS 1.02 limit | `--size 399 --tos 1.02` | Three 128 MB partitions | TOS 1.02, ACSI | All three drivers OK |
| 8 KB sectors | `--size 1024 --tos 1.04` | Largest logical sectors | TOS 2.06, ACSI | All three drivers OK |

Files copied onto images 22-27 were still there after a restart.

The original AHDI paused for 10-30 seconds at `Unit 0 -` on every machine, because of a bug in its slow ACSI delay. In Hatari on TOS 1.04, a one-byte fix cut the pause from about 30 seconds to about half a second, and the bundled AHDI now has that fix.

That fix did not change AHDI's result on the real TT, so the delay isn't why it finds no drives there.

## Changing drivers

1. Boot an image and copy files onto it.
2. Run `ahic install-driver --driver NAME` on it.
3. Boot again and confirm the new driver's banner and the copied files.

## Most likely problems

| Symptom | First thing to check |
|---|---|
| No banner, normal desktop without C: | Root or boot sector rejected, try `ahic verify` |
| AHDI hangs at boot | `HINSTALL.PRG`'s loader was built for AHDI 4.03 |
| Bombs right after the driver starts | Driver build uses 68030 instructions, see `docs/loaders.md` |
| Banner shows, but a partition is missing | Driver rejected its partition type or sector size |
| Only ICD fails on TT SCSI | Variant choice is inferred, see `docs/loaders.md` |
| Large partition corrupts | Lower the TOS limit in `internal/image/plan.go` |
