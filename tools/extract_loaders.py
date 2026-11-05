#!/usr/bin/env python3
"""Build assets/drivers.zip from the original driver and installer files.

Carves root and boot sector loaders out of each installer, checks them against
known code anchors, and packs them with the driver binaries and a manifest.
The offsets are explained in docs/loaders.md.

Usage: extract_loaders.py [third_party_dir] [output_zip]
"""
import hashlib, json, struct, sys, zipfile
from pathlib import Path

ROOT_RANGE = 0x1C2  # the root sector's code area ends where hd_siz begins
BOOT_RANGE = 0x1FE  # the boot sector's code area ends at the checksum word


def unpack_pfx(prg):
    """Unpack an executable compressed with LArc's PFX 1.13 (-lz5- LZSS)."""
    if prg[0x1E:0x2A] != b"LArc's PFX 1":
        raise ValueError("not a PFX executable")
    pos = 0x1C + 392
    if prg[pos + 2:pos + 7] != b"-lz5-":
        raise ValueError("unexpected PFX method")
    size = struct.unpack("<I", prg[pos + 11:pos + 15])[0]
    src = pos + prg[pos] + 2
    out = bytearray(b" " * 4078)
    end = 4078 + size
    flags = bits = 0
    while len(out) < end:
        if not bits:
            flags, bits = prg[src], 8
            src += 1
        literal = flags & 1
        flags >>= 1
        bits -= 1
        if literal:
            out.append(prg[src])
            src += 1
            continue
        low, high = prg[src], prg[src + 1]
        src += 2
        window = low | (high & 0xF0) << 4
        distance = (len(out) - window) & 0xFFF or 4096
        start = len(out) - distance
        for i in range((high & 0x0F) + 3):
            out.append(out[start + i])
    return bytes(out[4078:end])


def sector_with(code, limit):
    if len(code) > limit:
        raise ValueError(f"loader is {len(code)} bytes, limit is {limit}")
    return code + bytes(512 - len(code))


def expect(blob, offset, data, name):
    if blob[offset:offset + len(data)] != data:
        raise ValueError(f"{name}: anchor missing at {offset:#x}")


def check_prg(prg, name):
    if prg[:2] != b"\x60\x1a":
        raise ValueError(f"{name}: not a GEMDOS executable")


def main():
    third = Path(sys.argv[1] if len(sys.argv) > 1 else "/shared/third_party_projects")
    project = Path(__file__).resolve().parent.parent
    output = Path(sys.argv[2]) if len(sys.argv) > 2 else project / "assets/drivers.zip"
    st = third / "Atari_ST_Sources"
    hdx = st / "C/Atari Corp/Atari ST Dev Kit/HDX/HDX"
    cbhd = st / "C/Claus Brod/CBHD457"
    icd = third / "icdp655a"
    files = {}

    # AHDI: both loaders sit in the HINSTALL.PRG text segment.
    text = (hdx / "HINSTALL.PRG").read_bytes()[0x1C:]
    root = text[0x17E8:0x17E8 + 0x198]
    boot = text[0x165C:0x165C + 0x18C]
    expect(root, 0xA2, b"\xB0\x7C\x12\x34", "AHDI root")
    expect(boot, 0x96, b"SHDRIVERSYS\0", "AHDI boot")
    expect(boot, 0x188, b"\x4E\xEA\x00\x20", "AHDI boot")
    files["loaders/ahdi_root.bin"] = sector_with(root, ROOT_RANGE)
    files["loaders/ahdi_boot.bin"] = sector_with(boot, BOOT_RANGE)

    # CBHD: root and boot sit next to each other in CBHDCONF.APP.
    app = (cbhd / "CBHDCONF.APP").read_bytes()
    root = app[0x28E30:0x28E30 + 0x156]
    boot = app[0x29030:0x29030 + 0x1F6]
    expect(root, 0x8A, b"\xB0\x7C\x12\x34", "CBHD root")
    expect(root, 0x152, b"CBHD", "CBHD root")
    expect(boot, 0x1D4, b"CBHD    OLDOSLOADERSYSCBHD    SYS\0", "CBHD boot")
    files["loaders/cbhd_root.bin"] = sector_with(root, ROOT_RANGE)
    files["loaders/cbhd_boot.bin"] = sector_with(boot, BOOT_RANGE)

    # ICD: one boot sector plus a root per bus, all in the packed INSTALL.PRG.
    install = unpack_pfx((icd / "INSTALL.PRG").read_bytes())
    acsi = install[0xABF0:0xABF0 + 318]
    scsi = install[0xAD2E:0xAD2E + 146]
    boot = install[0xAFAC:0xAFAC + 464]
    expect(acsi, 0x44, b"\xB0\x7C\x12\x34", "ICD ACSI root")
    expect(scsi, 0x4E, b"\xB0\x7C\x12\x34", "ICD SCSI root")
    expect(boot, 0x22, b"ICDBOOT SYS\0", "ICD boot")
    files["loaders/icd_root_acsi.bin"] = sector_with(acsi, ROOT_RANGE)
    files["loaders/icd_root_scsi.bin"] = sector_with(scsi, ROOT_RANGE)
    files["loaders/icd_boot.bin"] = sector_with(boot, BOOT_RANGE)

    # Drivers keep their original bytes and take the name their loader expects.
    for target, source in {
        "drivers/SHDRIVER.SYS": third / "ADHI 6061 Sources/AHDI6061.PRG",
        "drivers/CBHD.SYS": cbhd / "CBHD.PRG",
        "drivers/ICDBOOT.SYS": icd / "ICDBOOT.PRG",
    }.items():
        data = source.read_bytes()
        check_prg(data, target)
        files[target] = data

    manifest = {name: hashlib.sha256(data).hexdigest() for name, data in sorted(files.items())}
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in sorted(files.items()):
            archive.writestr(name, data)
        archive.writestr("manifest.json", json.dumps(manifest, indent=2) + "\n")
    for name, digest in manifest.items():
        print(f"{digest[:12]}  {len(files[name]):6d}  {name}")
    print(f"wrote {output}")


if __name__ == "__main__":
    main()
