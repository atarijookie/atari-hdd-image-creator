#!/usr/bin/env python3
"""Run the real root and boot loaders from an image in a 68000-family emulator.

This fakes just enough of TOS and the ACSI DMA chip to reach the driver. It
checks the loaders, not the drivers: the run stops at the driver's first
instruction and compares what is in memory with the relocated driver file.

Usage: simulate_boot.py IMAGE DRIVER_FILE {acsi|dmaread} TOS_VERSION
Needs: pip install unicorn
"""
import struct, sys
from unicorn import Uc, UC_ARCH_M68K, UC_MODE_BIG_ENDIAN, UC_HOOK_CODE, UC_HOOK_INTR, UC_HOOK_MEM_WRITE
from unicorn.m68k_const import UC_M68K_REG_A0, UC_M68K_REG_A7, UC_M68K_REG_D0, UC_M68K_REG_D3, UC_M68K_REG_D4, UC_M68K_REG_D5, UC_M68K_REG_D7, UC_M68K_REG_PC, UC_M68K_REG_SR

RAM = 0x400000
ROOT_BUFFER = 0x2000
STACK = 0x1F00
OSHEADER = 0x1000
RETURN = 0xF00
LOAD_BASE = 0x100000
DMA_DATA = 0xFFFF8604
DMA_ADDRESS = (0xFFFF8609, 0xFFFF860B, 0xFFFF860D)


class Boot:
    def __init__(self, image, mode, version):
        self.image = image
        self.mode = mode
        self.reads = []
        self.calls = []
        self.command = []
        self.dma = [0, 0, 0]
        self.steps = 0
        self.outcome = None
        cpu = Uc(UC_ARCH_M68K, UC_MODE_BIG_ENDIAN)
        cpu.ctl_set_cpu_model(3)  # 68030-compatible, TOS 3.x machines use it too
        cpu.mem_map(0, RAM)
        cpu.mem_map(0xFFFF0000, 0x10000)
        self.cpu = cpu
        major, minor = version.split(".")
        cpu.mem_write(OSHEADER, struct.pack(">HH", 0x602E, int(major) << 8 | int(minor)))
        cpu.mem_write(0x4F2, struct.pack(">I", OSHEADER))
        cpu.mem_write(RETURN, b"\x4E\x71")
        cpu.hook_add(UC_HOOK_CODE, self.on_instruction)
        cpu.hook_add(UC_HOOK_INTR, self.on_trap)
        cpu.hook_add(UC_HOOK_MEM_WRITE, self.on_write, begin=0xFFFF8600, end=0xFFFF8610)

    def read_sectors(self, sector, count, address):
        self.reads.append((sector, count))
        self.cpu.mem_write(address, self.image[sector * 512:(sector + count) * 512])

    def on_write(self, uc, access, address, size, value, data):
        if address in DMA_ADDRESS:
            self.dma[DMA_ADDRESS.index(address)] = value & 0xFF
        elif address == DMA_DATA and size == 4:
            self.command.append(value >> 16 & 0xFF)
            if len(self.command) == 6:
                c = self.command
                self.command = []
                if c[0] & 0x1F != 8:
                    raise RuntimeError(f"unexpected ACSI command {c}")
                base = self.dma[0] << 16 | self.dma[1] << 8 | self.dma[2]
                self.read_sectors((c[1] & 0x1F) << 16 | c[2] << 8 | c[3], c[4], base)

    def arg(self, offset, fmt):
        sp = self.cpu.reg_read(UC_M68K_REG_A7)
        return struct.unpack(fmt, self.cpu.mem_read(sp + offset, struct.calcsize(fmt)))[0]

    def gemdos(self):
        call = self.arg(0, ">H")
        self.calls.append(call)
        if call == 0x48:
            size = self.arg(2, ">i")
            result = 0x200000 if size == -1 else LOAD_BASE
        elif call == 0x44:
            result = -32  # no Mxalloc before TOS 2.06 on plain STs
        elif call in (0x49, 0x4A, 0x0E):
            result = 0
        else:
            raise RuntimeError(f"unhandled GEMDOS call {call:#x}")
        self.cpu.reg_write(UC_M68K_REG_D0, result & 0xFFFFFFFF)

    def bios(self):
        if self.arg(0, ">H") != 11:
            raise RuntimeError("unhandled BIOS call")
        self.cpu.reg_write(UC_M68K_REG_D0, 0)  # Kbshift: no key held

    def xbios(self):
        if self.arg(0, ">H") != 42:
            raise RuntimeError("unhandled XBIOS call")
        sector, count, buffer = self.arg(2, ">I"), self.arg(6, ">H"), self.arg(8, ">I")
        self.read_sectors(sector, count, buffer)
        self.cpu.reg_write(UC_M68K_REG_D0, 0)

    def on_trap(self, uc, number, data):
        handlers = {33: self.gemdos, 45: self.bios, 46: self.xbios}
        handlers[number]()
        uc.reg_write(UC_M68K_REG_PC, self.current + 2)  # resume after the trap

    def on_instruction(self, uc, address, size, data):
        self.steps += 1
        self.current = address
        if self.steps % 64 == 0:
            ticks = struct.unpack(">I", uc.mem_read(0x4BA, 4))[0]
            uc.mem_write(0x4BA, struct.pack(">I", ticks + 1))
        if address == RETURN:
            self.outcome = "returned to ROM without booting"
            uc.emu_stop()
        elif address in (LOAD_BASE, LOAD_BASE + 0x20):
            self.outcome = address
            uc.emu_stop()

    def run(self):
        cpu = self.cpu
        cpu.mem_write(ROOT_BUFFER, self.image[:512])
        cpu.reg_write(UC_M68K_REG_SR, 0x2700)
        cpu.reg_write(UC_M68K_REG_A7, STACK - 4)
        cpu.mem_write(STACK - 4, struct.pack(">I", RETURN))
        cpu.reg_write(UC_M68K_REG_D7, 0)
        if self.mode == "dmaread":
            cpu.reg_write(UC_M68K_REG_D3, 0x444D4172)  # 'DMAr'
            cpu.reg_write(UC_M68K_REG_D4, 0)
            cpu.reg_write(UC_M68K_REG_D5, 0x80)
        cpu.emu_start(ROOT_BUFFER, 0, count=20_000_000)
        return self.outcome


def relocated(driver, base):
    """Return the driver's text and data as they should look once relocated."""
    text, data, _, symbols = struct.unpack(">IIII", driver[2:18])
    body = bytearray(driver[0x1C:0x1C + text + data])
    table = 0x1C + text + data + symbols
    offset = struct.unpack(">I", driver[table:table + 4])[0]
    table += 4
    delta = base + 0x1C
    if offset:
        while True:
            value = struct.unpack(">I", body[offset:offset + 4])[0]
            body[offset:offset + 4] = struct.pack(">I", (value + delta) & 0xFFFFFFFF)
            while True:
                step = driver[table]
                table += 1
                if step != 1:
                    break
                offset += 254
            if step == 0:
                break
            offset += step
    return bytes(body)


def main():
    image_path, driver_path, mode, version = sys.argv[1:5]
    image = open(image_path, "rb").read()
    driver = open(driver_path, "rb").read()
    boot = Boot(image, mode, version)
    outcome = boot.run()
    if not isinstance(outcome, int):
        print(f"FAIL {image_path}: {outcome}, {boot.steps} instructions, reads {boot.reads[:6]}")
        return 1
    expected = relocated(driver, LOAD_BASE)
    actual = bytes(boot.cpu.mem_read(LOAD_BASE + 0x1C, len(expected)))
    if actual != expected:
        wrong = next(i for i in range(len(expected)) if actual[i] != expected[i])
        print(f"FAIL {image_path}: driver differs at byte {wrong:#x}")
        return 1
    print(f"PASS {image_path}: entered driver at {outcome:#x} after {boot.steps} instructions, {len(boot.reads)} disk reads, {mode}, TOS {version}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
