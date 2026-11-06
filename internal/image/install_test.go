package image

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

var testTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func newImage(t *testing.T, sizeMB int, name, bus string) string {
	t.Helper()
	tos := "1.04"
	if bus == "scsi" {
		tos = "3.06"
	}
	plan, err := NewPlan(sizeMB, SmallBootMB, mustTOS(t, tos))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := Create(Options{Path: path, Plan: plan, Driver: mustDriver(t, name, bus), Now: testTime}); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustDriver(t *testing.T, name, bus string) *drivers.Driver {
	t.Helper()
	driver, err := drivers.Load(name, bus)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

// edit opens a partition, applies change and writes the result.
func edit(t *testing.T, path string, index int, change func(v *volume)) {
	t.Helper()
	d, err := openDisk(path, os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	v, err := openVolume(d.file, d.entries[index].Start)
	if err != nil {
		t.Fatal(err)
	}
	change(v)
	if err := v.commit(); err != nil {
		t.Fatal(err)
	}
}

func addFile(t *testing.T, v *volume, name string, data []byte) {
	t.Helper()
	if err := v.addFile(name, data, testTime); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, path string, index int, name string) []byte {
	t.Helper()
	d, err := openDisk(path, os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	v, err := openVolume(d.file, d.entries[index].Start)
	if err != nil {
		t.Fatal(err)
	}
	offset := v.findEntry(name)
	if offset < 0 {
		return nil
	}
	data, err := v.readFile(offset)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireVerified(t *testing.T, path string) {
	t.Helper()
	list, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range list {
		if check.Level != Pass {
			t.Errorf("verify: %s: %s", check.Name, check.Detail)
		}
	}
}

func requireFailure(t *testing.T, path, name string) {
	t.Helper()
	list, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range list {
		if check.Level == Fail && check.Name == name {
			return
		}
	}
	t.Errorf("verify did not fail %q: %+v", name, list)
}

func TestFreshImagesVerify(t *testing.T) {
	for _, name := range drivers.Names() {
		requireVerified(t, newImage(t, 64, name, "acsi"))
		requireVerified(t, newImage(t, 1024, name, "scsi"))
	}
}

func TestSwitchingDriversKeepsData(t *testing.T) {
	boot := bytes.Repeat([]byte("boot"), 5000)
	data := bytes.Repeat([]byte("data"), 5000)
	for _, from := range drivers.Names() {
		for _, to := range drivers.Names() {
			path := newImage(t, 64, from, "acsi")
			edit(t, path, 0, func(v *volume) { addFile(t, v, "DESKTOP.INF", boot) })
			edit(t, path, 1, func(v *volume) { addFile(t, v, "DATA.BIN", data) })
			driver := mustDriver(t, to, "acsi")
			if _, err := Install(path, driver, testTime); err != nil {
				t.Fatal(err)
			}
			requireVerified(t, path)
			if !bytes.Equal(readBack(t, path, 0, "DESKTOP.INF"), boot) {
				t.Errorf("%s to %s: boot partition file changed", from, to)
			}
			if !bytes.Equal(readBack(t, path, 1, "DATA.BIN"), data) {
				t.Errorf("%s to %s: data partition file changed", from, to)
			}
			if !bytes.Equal(readBack(t, path, 0, driver.FileName), driver.Binary) {
				t.Errorf("%s to %s: new driver is wrong", from, to)
			}
			if from != to && readBack(t, path, 0, mustDriver(t, from, "acsi").FileName) != nil {
				t.Errorf("%s to %s: old driver is still there", from, to)
			}
		}
	}
}

func TestInstallKeepsPartitionTable(t *testing.T) {
	path := newImage(t, 512, "ahdi", "acsi")
	before, _ := os.ReadFile(path)
	if _, err := Install(path, mustDriver(t, "icd", "acsi"), testTime); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before[hdSizeOffset:checksumOffset], after[hdSizeOffset:checksumOffset]) {
		t.Error("partition table changed")
	}
	info, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.RootLoader != "icd" || info.RootBus != "acsi" || info.BootLoader != "icd" {
		t.Errorf("info: %+v", info)
	}
}

func TestChangedDriverIsKeptAsOld(t *testing.T) {
	path := newImage(t, 64, "cbhd", "acsi")
	changed := []byte("\x60\x1Acustom driver")
	edit(t, path, 0, func(v *volume) {
		if err := v.removeFile(v.findEntry("CBHD.SYS")); err != nil {
			t.Fatal(err)
		}
		addFile(t, v, "CBHD.SYS", changed)
	})
	driver := mustDriver(t, "cbhd", "acsi")
	result, err := Install(path, driver, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if result.Renamed != "CBHD.OLD" || !bytes.Equal(readBack(t, path, 0, "CBHD.OLD"), changed) {
		t.Errorf("changed driver was not kept: %+v", result)
	}
	if !bytes.Equal(readBack(t, path, 0, "CBHD.SYS"), driver.Binary) {
		t.Error("new driver is wrong")
	}
	edit(t, path, 0, func(v *volume) {
		if err := v.removeFile(v.findEntry("CBHD.SYS")); err != nil {
			t.Fatal(err)
		}
		addFile(t, v, "CBHD.SYS", changed)
	})
	if _, err := Install(path, driver, testTime); err == nil {
		t.Error("a second changed driver must not replace CBHD.OLD")
	}
}

func TestFailedInstallChangesNothing(t *testing.T) {
	path := newImage(t, 4, "ahdi", "acsi")
	edit(t, path, 0, func(v *volume) {
		addFile(t, v, "FILLER.BIN", make([]byte, (v.freeClusters()-2)*v.clusterBytes()))
	})
	before, _ := os.ReadFile(path)
	if _, err := Install(path, mustDriver(t, "icd", "acsi"), testTime); err == nil {
		t.Fatal("expected no space for the driver")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("a failed install changed the image")
	}
}

func TestFreeEntryIsBeforeTheEndMarker(t *testing.T) {
	path := newImage(t, 64, "icd", "acsi")
	edit(t, path, 0, func(v *volume) {
		addFile(t, v, "A.TXT", []byte("a"))
		addFile(t, v, "B.TXT", []byte("b"))
		if err := v.removeFile(v.findEntry("A.TXT")); err != nil {
			t.Fatal(err)
		}
	})
	edit(t, path, 0, func(v *volume) {
		addFile(t, v, "C.TXT", []byte("c"))
		if v.findEntry("C.TXT") != entrySize {
			t.Errorf("deleted slot was not reused, entry is at %d", v.findEntry("C.TXT"))
		}
	})
}

func TestInstallRefusesICDExtendedTable(t *testing.T) {
	path := newImage(t, 64, "ahdi", "acsi")
	corrupt(t, path, func(root []byte) {
		copy(root, make([]byte, icdTable)) // an unknown root loader
		entry := root[icdTable:]
		entry[0] = flagExists
		copy(entry[1:4], "BGM")
		makeExecutable(root, rootSpareOffset)
	})
	if _, err := Install(path, mustDriver(t, "cbhd", "acsi"), testTime); err == nil {
		t.Error("expected a refusal")
	}
}

func TestInstallRefusesDOSTable(t *testing.T) {
	path := newImage(t, 64, "ahdi", "acsi")
	corrupt(t, path, func(root []byte) {
		binary.BigEndian.PutUint16(root[checksumOffset:], dosSignature)
	})
	if _, err := Install(path, mustDriver(t, "cbhd", "acsi"), testTime); err == nil {
		t.Error("expected a refusal")
	}
}

func TestInstallMarksBootPartition(t *testing.T) {
	path := newImage(t, 64, "ahdi", "acsi")
	corrupt(t, path, func(root []byte) {
		root[partitionTable] = flagExists
		binary.BigEndian.PutUint32(root[hdSizeOffset:], 0)
		makeExecutable(root, rootSpareOffset)
	})
	if _, err := Install(path, mustDriver(t, "cbhd", "acsi"), testTime); err != nil {
		t.Fatal(err)
	}
	requireVerified(t, path)
}

// corrupt rewrites the root sector in place.
func corrupt(t *testing.T, path string, change func(root []byte)) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	root := make([]byte, sectorSize)
	if _, err := file.ReadAt(root, 0); err != nil {
		t.Fatal(err)
	}
	change(root)
	if _, err := file.WriteAt(root, 0); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyFindsDamage(t *testing.T) {
	path := newImage(t, 64, "ahdi", "acsi")
	corrupt(t, path, func(root []byte) { root[0] ^= 0xFF })
	requireFailure(t, path, "Root sector checksum")

	path = newImage(t, 64, "ahdi", "acsi")
	corrupt(t, path, func(root []byte) {
		root[partitionTable] = flagExists
		makeExecutable(root, rootSpareOffset)
	})
	requireFailure(t, path, "A partition is bootable")

	path = newImage(t, 64, "ahdi", "acsi")
	edit(t, path, 0, func(v *volume) {
		if err := v.removeFile(v.findEntry("SHDRIVER.SYS")); err != nil {
			t.Fatal(err)
		}
	})
	requireFailure(t, path, "Driver file exists")

	path = newImage(t, 64, "ahdi", "acsi")
	edit(t, path, 0, func(v *volume) {
		clusters, err := v.chain(v.findEntry("SHDRIVER.SYS"))
		if err != nil {
			t.Fatal(err)
		}
		v.fat[clusters[len(clusters)-1]] = 0xFFF8
	})
	requireFailure(t, path, "Driver chain ends in 0xFFFF")

	path = newImage(t, 64, "ahdi", "acsi")
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.WriteAt([]byte{0x12}, 3*sectorSize+100) // first FAT only
	requireFailure(t, path, "Partition 1 FAT copies match")
}
