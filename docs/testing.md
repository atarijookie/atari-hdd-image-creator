# Test plan

No `ahic` image has been booted on TOS yet. The loader simulation stops as soon as a driver starts, so these tests cover what it can't.

## Creating test images

```sh
ahic create --size 64 --tos 1.02 --driver ahdi ahdi-102.img
ahic create --size 64 --tos 1.02 --driver cbhd cbhd-102.img
ahic create --size 64 --tos 1.02 --driver icd icd-102.img
ahic create --size 1024 --tos 3.06 --bus scsi --driver ahdi ahdi-tt.img
```

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

Fill in each cell with the date and the result.

| TOS, machine, bus | AHDI | CBHD | ICD |
|---|---|---|---|
| 1.02, ST, ACSI | Not tested | Not tested | Not tested |
| 1.04, ST, ACSI | Not tested | Not tested | Not tested |
| 2.06, ST, ACSI | Not tested | Not tested | Not tested |
| 3.0x, TT, ACSI | Not tested | Not tested | Not tested |
| 3.0x, TT, SCSI | Not tested | Not tested | Not tested |

## Layouts to test

| Layout | Command | Why |
|---|---|---|
| Smallest | `--size 4` | Few clusters on FAT16 |
| Small BGM | `--size 40` | BGM with 512-byte sectors |
| TOS 1.02 limit | `--size 399 --tos 1.02` | Three 128 MB partitions |
| 8 KB sectors | `--size 1024 --tos 1.04` | Largest logical sectors |

## Changing drivers

1. Boot an image and copy files onto it.
2. Run `ahic install-driver --driver NAME` on it.
3. Boot again and confirm the new driver's banner and the copied files.

## Most likely problems

| Symptom | First thing to check |
|---|---|
| No banner, normal desktop without C: | Root or boot sector rejected, try `ahic verify` |
| AHDI 6.x hangs at boot | `HINSTALL.PRG`'s loader was built for AHDI 4.03 |
| Banner shows, but a partition is missing | Driver rejected its partition type or sector size |
| Only ICD fails on TT SCSI | Variant choice is inferred, see `docs/loaders.md` |
| Large partition corrupts | Lower the TOS limit in `internal/image/plan.go` |
