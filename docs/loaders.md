# Driver loader analysis

This analysis comes from reading the loader code. `tools/simulate_boot.py` runs those loaders against generated images, and `docs/testing.md` records real boot results.

`tools/extract_loaders.py` carves the loaders out of the original installers and packs them, with the drivers, into `assets/drivers.zip`.

## Boot chain

1. The ROM reads physical sector 0 and runs it if its words sum to `0x1234`.
2. The root code picks the bootable partition and reads that partition's sector 0.
3. If that sector also sums to `0x1234`, the root code runs it.
4. The boot sector finds the driver file in the root directory and follows its FAT chain.
5. It applies the GEMDOS relocation table and jumps to the driver.

## What each driver uses

| | AHDI | CBHD | ICD PRO |
|---|---|---|---|
| Driver file | `SHDRIVER.SYS` | `CBHD.SYS` | `ICDBOOT.SYS` |
| Driver source | `DRIVER.PRG` from `AHD6060S.ZIP` | `CBHD.PRG` 4.57 | `ICDBOOT.PRG` 6.5.5 |
| Loaders from | `HINSTALL.PRG` | `CBHDCONF.APP` | `INSTALL.PRG`, after unpacking PFX |
| Root code | One, 408 bytes | One, 342 bytes | **ACSI 318 or SCSI 146 bytes** |
| Boot code | 396 bytes | 501 bytes | 464 bytes |
| Driver entry | Load address + `0x20` | Load address + `0x20` | Load address, with `d0 = 'WBLB'` |
| Read routine passed in | `a3` | `a3` | `a5` |

`DRIVER.PRG` is AHDI 6.05c, going by its source history, though its banner says `v6.00`. `tools/extract_loaders.py` changes one byte in it: the `bge.s sdelay` branch in `ACSI.S` becomes `6CFA`, so the delay loops on its compare and not its add. Unfixed, it keeps adding `_hz_200` until the number overflows, which pauses boot for up to 30 seconds.

**`AHDI6061.PRG` must not be used on a 68000.** It was assembled for the 68030 and has 43 PC-relative `tst` instructions, which a 68000 treats as illegal. It crashed with bombs on every ST test. Before bundling a new driver build, disassemble it with `-m m68k:68000` to check for this. `simulate_boot.py` can't catch it, because its emulator accepts 68030 instructions in 68000 code.

## Offsets

| Loader | Source | Offset | Length |
|---|---|---|---|
| AHDI root | `HINSTALL.PRG` text segment | `0x17E8` | `0x198` |
| AHDI boot | `HINSTALL.PRG` text segment | `0x165C` | `0x18C` |
| CBHD root | `CBHDCONF.APP` file | `0x28E30` | `0x156` |
| CBHD boot | `CBHDCONF.APP` file | `0x29030` | `0x1F6` |
| ICD ACSI root | Unpacked `INSTALL.PRG` file | `0xABF0` | `318` |
| ICD SCSI root | Unpacked `INSTALL.PRG` file | `0xAD2E` | `146` |
| ICD boot | Unpacked `INSTALL.PRG` file | `0xAFAC` | `464` |

`SHDRIVER.BS` in `Atari_ST_Sources` is a second copy of the AHDI boot sector with equivalent logic. We use the `HINSTALL.PRG` copy so root and boot come from the same installer.

## Writing the sectors

- **Root sector:** loader code first, then `hd_siz` at `0x1C2`, the partition table at `0x1C6`, and the bad sector fields at `0x1F6`.
- **Boot sector:** bytes `0x00-0x0A` and `0x1E` onward come from the loader, and our BPB fills `0x0B-0x1D`.
- **Checksum:** set the last word of both sectors so all words sum to `0x1234`.
- **CBHD template:** the eight `BGM` entries after `0x156` in `CBHDCONF.APP` are not carved, so that table stays empty.

## Image rules the loaders require

| Rule | Reason |
|---|---|
| Boot partition `p_flg = 0x81` | CBHD and ICD test both bits |
| `hd_siz` must not be zero | ICD skips the disk otherwise |
| Boot partition uses FAT16, including small images | All three loaders read 16-bit entries, and AHDI forces FAT16 for GEMDOS partitions |
| Exactly 2 FATs | All three loaders calculate `2 × spf` |
| 2 sectors per cluster | ICD hardcodes this when calculating file positions |
| Logical sector size is a multiple of 512 | All three convert it to physical sectors |
| Root directory exactly fills whole logical sectors | AHDI and CBHD do not round its size up, so data start would be wrong otherwise |
| End of chain is `0xFFFF` | ICD stops only on `0xFFFF` |
| Driver entry comes before the first unused root directory entry | ICD stops searching at an entry starting with `0` |
| No `OSLOADER.SYS` next to CBHD | CBHD loads that file first, for MagiC |
| Fewer than `0x8000` clusters | AHDI and CBHD treat a high bit as end of chain |

The file does not need to be contiguous, because all three follow the FAT.

## Bus and TOS behaviour

- **AHDI and CBHD** use one root for both buses. They use XBIOS `DMAread` when `d3 = 'DMAr'`, otherwise they drive ACSI directly. The ROM setting `d3` is inferred from this code.
- **ICD** needs a root chosen by bus. The ACSI root drives the DMA hardware directly. The SCSI root checks for TOS 2.00 or newer, then calls `DMAread` with device `(d7 >> 5) + 8`.
- **ICD selection is inferred:** `INSTALL.PRG` picks from five variants using flags that look like TT, Falcon and IDE tests. We use the ACSI or SCSI root with the first boot sector, leaving out the IDE root and the second boot sector, which appears to be for Falcon.
- **ICD on TOS 1.02:** its installer text says normal mode works with all TOS versions. The TOS 1.0-1.2 warning applies to its PC-compatible mode only.
- **Skip key:** holding Alternate makes the AHDI and ICD roots skip booting. CBHD's root asks `Abort?` instead.
- **ICD installers:** they require an ICD host adapter, but `ahic` doesn't run them. The PRO driver's documentation says it works without one.

## Still to verify by booting

- AHDI 6.05c on any TOS, and all three drivers on the TT.
- ICD's SCSI root and boot sector pairing on TT.
- Whether `HINSTALL.PRG`'s boot sector works for AHDI 6.x. The installer ships with the older AHDI 4.03, though the entry point matches and AHDI 6.05c ran to completion on a simulated 68000 with fake hardware.
- FAT16 on partitions under 4085 clusters in other FAT readers, which may assume FAT12. All three drivers booted such an image.
