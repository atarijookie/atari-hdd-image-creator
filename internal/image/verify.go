package image

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

// Level grades one verify check.
type Level int

// Check levels.
const (
	Pass Level = iota
	Warn
	Fail
)

// Check is one verify result.
type Check struct {
	Level  Level
	Name   string
	Detail string
}

type checks []Check

func (c *checks) add(ok bool, level Level, name, problem string) {
	if ok {
		*c = append(*c, Check{Pass, name, ""})
	} else {
		*c = append(*c, Check{level, name, problem})
	}
}

// Failed reports whether any check failed.
func Failed(list []Check) bool {
	for _, check := range list {
		if check.Level == Fail {
			return true
		}
	}
	return false
}

// Verify checks that an image follows the rules in docs/loaders.md.
func Verify(path string) ([]Check, error) {
	d, err := openDisk(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer d.close()
	var list checks
	rootName, err := verifyRoot(d, &list)
	if err != nil {
		return nil, err
	}
	bootIndex := verifyPartitions(d, &list)
	if bootIndex < 0 {
		return list, nil
	}
	if err := verifyBoot(d, d.entries[bootIndex], rootName, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func verifyRoot(d *disk, list *checks) (string, error) {
	sum := sectorSum(d.root)
	list.add(sum == bootableMagic, Fail, "Root sector checksum", fmt.Sprintf("sum is %#04x, needs 0x1234", sum))
	list.add(!d.hasDOSSignature(), Fail, "Root sector is not DOS format", "last word is the DOS signature")
	sectors := binary.BigEndian.Uint32(d.root[hdSizeOffset:])
	list.add(sectors != 0, Fail, "Disk size is set", "hd_siz is zero, ICD would skip this disk")
	list.add(int64(sectors)*sectorSize <= d.size, Fail, "Disk size fits the image",
		fmt.Sprintf("hd_siz is %d sectors, image has %d", sectors, d.size/sectorSize))
	name, _, found, err := drivers.IdentifyRoot(d.root)
	if err != nil {
		return "", err
	}
	list.add(found, Warn, "Root loader is known", "it matches no bundled driver")

	start := binary.BigEndian.Uint32(d.root[badStartOffset:])
	count := binary.BigEndian.Uint32(d.root[badCountOffset:])
	if count > 0 && (int64(start)+int64(count))*sectorSize <= d.size {
		data := make([]byte, int(count)*sectorSize)
		if _, err := d.file.ReadAt(data, int64(start)*sectorSize); err != nil {
			return "", err
		}
		var bytesum byte
		for _, value := range data {
			bytesum += value
		}
		list.add(bytesum == 0xA5, Warn, "Bad sector list checksum", fmt.Sprintf("sum is %#02x, needs 0xA5", bytesum))
	}
	return name, nil
}

// verifyPartitions checks each partition and returns the boot index, or -1.
func verifyPartitions(d *disk, list *checks) int {
	end := uint32(0)
	bootable := 0
	present := 0
	for i, entry := range d.entries {
		if !entry.Exists() {
			continue
		}
		present++
		if entry.Bootable() {
			bootable++
		}
		name := fmt.Sprintf("Partition %d", i+1)
		list.add((int64(entry.Start)+int64(entry.Size))*sectorSize <= d.size, Fail, name+" fits the image", "it runs past the end")
		list.add(entry.Start >= end, Fail, name+" does not overlap", "it starts inside the previous one")
		end = entry.Start + entry.Size
		if !entry.Mountable() {
			list.add(false, Warn, name+" type", fmt.Sprintf("type %q is not checked", entry.Type))
			continue
		}
		v, err := openVolume(d.file, entry.Start)
		if err != nil {
			list.add(false, Fail, name+" filesystem", err.Error())
			continue
		}
		list.add(int64(v.bpb.Sectors)*int64(v.bpb.SectorSize) <= int64(entry.Size)*sectorSize, Fail,
			name+" filesystem fits", "it is larger than the partition")
		same, err := fatCopiesMatch(v)
		if err != nil {
			list.add(false, Fail, name+" FAT copies match", err.Error())
			continue
		}
		list.add(same, Fail, name+" FAT copies match", "the two FATs differ")
	}
	list.add(present > 0, Fail, "Partitions exist", "the table is empty")
	list.add(bootable <= 1, Warn, "One bootable partition", fmt.Sprintf("%d are marked, the first one boots", bootable))
	index := d.bootEntry()
	list.add(index >= 0, Fail, "A partition is bootable", "none has flag 0x81")
	return index
}

func verifyBoot(d *disk, entry Entry, rootName string, list *checks) error {
	if !entry.Mountable() {
		list.add(false, Fail, "Boot partition type", fmt.Sprintf("type %q cannot boot", entry.Type))
		return nil
	}
	sector := make([]byte, sectorSize)
	if _, err := d.file.ReadAt(sector, int64(entry.Start)*sectorSize); err != nil {
		list.add(false, Fail, "Boot sector readable", err.Error())
		return nil
	}
	sum := sectorSum(sector)
	list.add(sum == bootableMagic, Fail, "Boot sector checksum", fmt.Sprintf("sum is %#04x, needs 0x1234", sum))
	name, found, err := drivers.IdentifyBoot(sector)
	if err != nil {
		return err
	}
	list.add(found, Warn, "Boot loader is known", "it matches no bundled driver")
	if !found {
		return nil
	}
	if rootName != "" {
		list.add(rootName == name, Fail, "Root and boot loaders match",
			fmt.Sprintf("root is %s, boot is %s", rootName, name))
	}
	return verifyDriverFile(d, entry, name, list)
}

func verifyDriverFile(d *disk, entry Entry, name string, list *checks) error {
	v, err := openVolume(d.file, entry.Start)
	if err != nil {
		return nil // already reported by verifyPartitions
	}
	driver, err := drivers.Load(name, "acsi")
	if err != nil {
		return err
	}
	offset := v.findEntry(driver.FileName)
	list.add(offset >= 0, Fail, "Driver file exists", driver.FileName+" is missing")
	if offset < 0 {
		return nil
	}
	data, err := v.readFile(offset)
	if err != nil {
		list.add(false, Fail, "Driver file readable", err.Error())
		return nil
	}
	list.add(len(data) > 2 && data[0] == 0x60 && data[1] == 0x1A, Fail, "Driver is an executable", "it has no GEMDOS header")
	list.add(bytes.Equal(data, driver.Binary), Warn, "Driver matches the bundled copy", "the file differs")
	clusters, err := v.chain(offset)
	if err != nil {
		return nil
	}
	last := binary.LittleEndian.Uint16(v.entry(offset)[26:])
	if len(clusters) > 0 {
		last = clusters[len(clusters)-1]
	}
	list.add(v.fat[last] == endOfChain, Fail, "Driver chain ends in 0xFFFF",
		fmt.Sprintf("it ends in %#04x, which ICD's loader misreads", v.fat[last]))
	if name == "cbhd" {
		list.add(v.findEntry(magicLoader) < 0, Warn, "No "+magicLoader, "CBHD's loader will load it instead of CBHD.SYS")
	}
	return nil
}
