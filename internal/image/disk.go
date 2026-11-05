package image

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

// Entry is one partition table entry.
type Entry struct {
	Flags byte
	Type  string
	Start uint32
	Size  uint32
}

// Exists reports whether the entry is in use.
func (e Entry) Exists() bool { return e.Flags&flagExists != 0 }

// Bootable reports whether root loaders would pick this entry.
func (e Entry) Bootable() bool {
	return e.Flags&(flagExists|flagBootable) == flagExists|flagBootable
}

// Mountable reports whether the entry is a GEMDOS partition.
func (e Entry) Mountable() bool {
	return e.Exists() && (e.Type == "GEM" || e.Type == "BGM")
}

// disk is an image's root sector and size.
type disk struct {
	file    *os.File
	size    int64
	root    []byte
	entries []Entry
}

func openDisk(path string, flag int) (*disk, error) {
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	d := &disk{file: file, root: make([]byte, sectorSize)}
	if info, err := file.Stat(); err != nil {
		file.Close()
		return nil, err
	} else {
		d.size = info.Size()
	}
	if _, err := file.ReadAt(d.root, 0); err != nil {
		file.Close()
		if err == io.EOF {
			return nil, fmt.Errorf("%s is too small to be a disk image", path)
		}
		return nil, err
	}
	for i := 0; i < maxPartitions; i++ {
		d.entries = append(d.entries, parseEntry(d.root[partitionTable+i*partitionEntry:]))
	}
	return d, nil
}

func parseEntry(data []byte) Entry {
	return Entry{
		Flags: data[0],
		Type:  string(data[1:4]),
		Start: binary.BigEndian.Uint32(data[4:]),
		Size:  binary.BigEndian.Uint32(data[8:]),
	}
}

func (d *disk) close() error { return d.file.Close() }

// bootEntry returns the index of the first bootable partition, or -1.
func (d *disk) bootEntry() int {
	for i, entry := range d.entries {
		if entry.Bootable() {
			return i
		}
	}
	return -1
}

// hasDOSSignature reports a root sector that drivers would read as DOS format.
func (d *disk) hasDOSSignature() bool {
	return binary.BigEndian.Uint16(d.root[checksumOffset:]) == dosSignature
}

// icdEntries counts partitions in ICD's extended table.
func (d *disk) icdEntries() int {
	count := 0
	for i := 0; i < icdTableCount; i++ {
		entry := parseEntry(d.root[icdTable+i*partitionEntry:])
		if entry.Mountable() || entry.Exists() && entry.Type == "XGM" {
			count++
		}
	}
	return count
}

// PartitionInfo describes one partition for display.
type PartitionInfo struct {
	Entry
	BPB       BPB
	Problem   string // why the filesystem could not be read
	FreeBytes int64
}

// Info describes an image for display.
type Info struct {
	Bytes       int64
	Sectors     uint32 // hd_siz
	RootLoader  string // driver name, or empty
	RootBus     string
	BootLoader  string
	DriverFile  string
	DriverBytes int
	Partitions  []PartitionInfo
	ICDEntries  int
}

// Inspect reads an image's partitions and installed driver.
func Inspect(path string) (*Info, error) {
	d, err := openDisk(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer d.close()
	info := &Info{Bytes: d.size, Sectors: binary.BigEndian.Uint32(d.root[hdSizeOffset:]), ICDEntries: d.icdEntries()}
	name, bus, found, err := drivers.IdentifyRoot(d.root)
	if err != nil {
		return nil, err
	}
	if found {
		info.RootLoader, info.RootBus = name, bus
	}
	boot := d.bootEntry()
	for i, entry := range d.entries {
		if !entry.Exists() {
			continue
		}
		partition := PartitionInfo{Entry: entry}
		if entry.Mountable() {
			if v, err := openVolume(d.file, entry.Start); err != nil {
				partition.Problem = err.Error()
			} else {
				partition.BPB = v.bpb
				partition.FreeBytes = int64(v.freeClusters()) * int64(v.clusterBytes())
				if i == boot {
					if err := info.inspectBoot(d, v, entry); err != nil {
						return nil, err
					}
				}
			}
		}
		info.Partitions = append(info.Partitions, partition)
	}
	return info, nil
}

func (info *Info) inspectBoot(d *disk, v *volume, entry Entry) error {
	sector := make([]byte, sectorSize)
	if _, err := d.file.ReadAt(sector, int64(entry.Start)*sectorSize); err != nil {
		return err
	}
	name, found, err := drivers.IdentifyBoot(sector)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	info.BootLoader = name
	for _, item := range drivers.Catalog() {
		if item.Name != name {
			continue
		}
		info.DriverFile = item.FileName
		if offset := v.findEntry(item.FileName); offset >= 0 {
			info.DriverBytes = int(binary.LittleEndian.Uint32(v.entry(offset)[28:]))
		}
	}
	return nil
}

// Letter returns the drive letter of the partition at index i of a mountable
// list. TOS assigns letters to GEM and BGM partitions in order, from C.
func driveLetter(index int) string {
	return strings.ToUpper(string(rune('C' + index)))
}

// DriveLetters names each listed partition, or "-" when it gets no letter.
func (info *Info) DriveLetters() []string {
	letters := make([]string, len(info.Partitions))
	next := 0
	for i, partition := range info.Partitions {
		letters[i] = "-"
		if partition.Mountable() {
			letters[i] = driveLetter(next)
			next++
		}
	}
	return letters
}
