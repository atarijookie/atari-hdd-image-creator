package image

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

func mustTOS(t *testing.T, version string) TOS {
	t.Helper()
	tos, err := FindTOS(version)
	if err != nil {
		t.Fatal(err)
	}
	return tos
}

func TestPlanLayouts(t *testing.T) {
	cases := []struct {
		size    int
		version string
		types   []string
	}{
		{4, "1.04", []string{"GEM"}},
		{31, "1.04", []string{"BGM"}},
		{32, "1.04", []string{"GEM", "BGM"}},
		{30, "1.04", []string{"BGM"}},
		{64, "1.04", []string{"GEM", "BGM"}},
		{399, "1.02", []string{"GEM", "BGM", "BGM", "BGM"}},
		{1024, "3.06", []string{"GEM", "BGM", "BGM"}},
		{1024, "1.04", []string{"GEM", "BGM", "BGM"}},
		{1010, "1.04", []string{"GEM", "BGM", "BGM"}},
	}
	for _, c := range cases {
		plan, err := NewPlan(c.size, mustTOS(t, c.version))
		if err != nil {
			t.Fatalf("%d MB: %v", c.size, err)
		}
		if len(plan.Partitions) != len(c.types) {
			t.Fatalf("%d MB: got %d partitions, want %d", c.size, len(plan.Partitions), len(c.types))
		}
		end := uint32(firstPartition)
		tos := mustTOS(t, c.version)
		for i, partition := range plan.Partitions {
			if partition.Type() != c.types[i] {
				t.Errorf("%d MB partition %d: type %s, want %s", c.size, i, partition.Type(), c.types[i])
			}
			if partition.Start != end {
				t.Errorf("%d MB partition %d: starts at %d, want %d", c.size, i, partition.Start, end)
			}
			if partition.Sectors > tos.MaxPartition {
				t.Errorf("%d MB partition %d: too large for TOS %s", c.size, i, tos.Version)
			}
			end += partition.Sectors
			checkGeometry(t, partition)
		}
		if end > plan.Sectors {
			t.Errorf("%d MB: partitions end at %d, image has %d", c.size, end, plan.Sectors)
		}
	}
}

func checkGeometry(t *testing.T, partition Partition) {
	t.Helper()
	g := partition.Geometry
	if rootEntries*32%g.LogicalSize != 0 {
		t.Errorf("root directory does not fill whole %d-byte sectors", g.LogicalSize)
	}
	if g.Clusters >= 0x7FFF {
		t.Errorf("%d clusters is too many", g.Clusters)
	}
	if (g.Clusters+2)*2 > g.FATSectors*g.LogicalSize {
		t.Errorf("FAT too small for %d clusters", g.Clusters)
	}
	if int(partition.Sectors) != g.Logical*(g.LogicalSize/sectorSize) {
		t.Errorf("partition is not a whole number of logical sectors")
	}
	if g.DataStart()+g.Clusters*sectorsPerCluster > g.Logical {
		t.Errorf("clusters run past the end of the partition")
	}
}

func TestPlanRejects(t *testing.T) {
	cases := []struct {
		size    int
		version string
	}{{3, "1.04"}, {1025, "3.06"}, {400, "1.02"}}
	for _, c := range cases {
		if _, err := NewPlan(c.size, mustTOS(t, c.version)); err == nil {
			t.Errorf("%d MB on TOS %s: expected an error", c.size, c.version)
		}
	}
}

func TestChecksumAvoidsDOSSignature(t *testing.T) {
	sector := make([]byte, sectorSize)
	binary.BigEndian.PutUint16(sector, 0xBC8A) // would need 0x55AA to reach 0x1234
	makeExecutable(sector, rootSpareOffset)
	if binary.BigEndian.Uint16(sector[checksumOffset:]) == dosSignature {
		t.Error("checksum word is the DOS signature")
	}
	if sectorSum(sector) != bootableMagic {
		t.Errorf("sum is %#x", sectorSum(sector))
	}
}

func TestCreatedImagesLoadTheDriver(t *testing.T) {
	cases := []struct {
		size         int
		version, bus string
	}{
		{4, "1.02", "acsi"},
		{64, "1.04", "acsi"},
		{1024, "3.06", "scsi"},
	}
	for _, name := range drivers.Names() {
		for _, c := range cases {
			driver, err := drivers.Load(name, c.bus)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := NewPlan(c.size, mustTOS(t, c.version))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "disk.img")
			options := Options{Path: path, Plan: plan, Driver: driver, Now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
			if err := Create(options); err != nil {
				t.Fatal(err)
			}
			checkImage(t, path, plan, driver)
		}
	}
}

// checkImage walks the image the way the loaders do, using physical sectors
// and only what the sectors themselves contain.
func checkImage(t *testing.T, path string, plan *Plan, driver *drivers.Driver) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	if info.Size() != int64(plan.Sectors)*sectorSize {
		t.Fatalf("image is %d bytes", info.Size())
	}
	read := func(sector uint32, count int) []byte {
		data := make([]byte, count*sectorSize)
		if _, err := file.ReadAt(data, int64(sector)*sectorSize); err != nil {
			t.Fatal(err)
		}
		return data
	}
	root := read(0, 1)
	if sectorSum(root) != bootableMagic {
		t.Fatalf("root sum %#x", sectorSum(root))
	}
	if !bytes.Equal(root[:hdSizeOffset], driver.Root[:hdSizeOffset]) {
		t.Error("root loader code was changed")
	}
	if binary.BigEndian.Uint32(root[hdSizeOffset:]) != plan.Sectors {
		t.Error("wrong hd_siz")
	}
	if root[partitionTable] != flagExists|flagBootable {
		t.Errorf("boot flag %#x", root[partitionTable])
	}
	bad := read(1, 1)
	var sum byte
	for _, value := range bad {
		sum += value
	}
	if sum != 0xA5 {
		t.Errorf("bad sector list sums to %#x", sum)
	}
	for i, partition := range plan.Partitions {
		boot := read(binary.BigEndian.Uint32(root[partitionTable+i*partitionEntry+4:]), 1)
		if i > 0 && sectorSum(boot) == bootableMagic {
			t.Errorf("data partition %d is executable", i)
		}
		if binary.LittleEndian.Uint16(boot[0x13:]) != uint16(partition.Geometry.Logical) {
			t.Errorf("partition %d has the wrong sector count", i)
		}
	}
	start := binary.BigEndian.Uint32(root[partitionTable+4:])
	boot := read(start, 1)
	if sectorSum(boot) != bootableMagic {
		t.Fatalf("boot sum %#x", sectorSum(boot))
	}
	if !bytes.Equal(boot[0x1E:bootSpareOffset], driver.Boot[0x1E:bootSpareOffset]) {
		t.Error("boot loader code was changed")
	}
	le := binary.LittleEndian
	ratio := uint32(boot[0x0C]) >> 1
	reserved := uint32(le.Uint16(boot[0x0E:])) * ratio
	fat := uint32(le.Uint16(boot[0x16:])) * ratio
	entries := uint32(le.Uint16(boot[0x11:]))
	rootDir := start + reserved + 2*fat
	data := rootDir + entries/16 // no rounding, as in AHDI and CBHD
	entry := read(rootDir, 1)[:32]
	if string(entry[:11]) != shortName(driver.FileName) {
		t.Fatalf("first entry is %q", entry[:11])
	}
	cluster := uint32(le.Uint16(entry[26:]))
	size := int(le.Uint32(entry[28:]))
	var loaded []byte
	for cluster != endOfChain {
		first := data + (cluster-2)*uint32(boot[0x0D])*ratio
		loaded = append(loaded, read(first, int(uint32(boot[0x0D])*ratio))...)
		fatSector := read(start+reserved+cluster>>8, 1)
		second := read(start+reserved+fat+cluster>>8, 1)
		offset := (cluster & 0xFF) * 2
		next := uint32(le.Uint16(fatSector[offset:]))
		if next != uint32(le.Uint16(second[offset:])) {
			t.Fatalf("FAT copies differ at cluster %d", cluster)
		}
		if next >= 0x8000 && next != endOfChain {
			t.Fatalf("chain ends in %#x", next)
		}
		cluster = next
	}
	if !bytes.Equal(loaded[:size], driver.Binary) {
		t.Error("loaded file differs from the driver")
	}
}

func TestCreateKeepsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver, err := drivers.Load("ahdi", "acsi")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(4, mustTOS(t, "1.04"))
	if err != nil {
		t.Fatal(err)
	}
	err = Create(Options{Path: path, Plan: plan, Driver: driver, Now: time.Now()})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "keep" {
		t.Error("existing file was changed")
	}
}
