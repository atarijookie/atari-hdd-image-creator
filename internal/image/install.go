package image

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

const magicLoader = "OSLOADER.SYS" // CBHD's loader prefers this file

// InstallResult reports what an install changed besides the driver itself.
type InstallResult struct {
	Removed  []string
	Renamed  string
	Warnings []string
}

// DetectBus returns the bus of an installed ICD root loader, or empty.
func DetectBus(path string) (string, error) {
	d, err := openDisk(path, os.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer d.close()
	_, bus, _, err := drivers.IdentifyRoot(d.root)
	return bus, err
}

// Install puts a driver on an existing image and keeps all other data. Every
// check runs before the first write.
func Install(path string, driver *drivers.Driver, now time.Time) (*InstallResult, error) {
	d, err := openDisk(path, os.O_RDWR)
	if err != nil {
		return nil, err
	}
	defer d.close()
	if d.hasDOSSignature() {
		return nil, errors.New("this image uses a DOS partition table, which ahic does not support")
	}
	if count := d.icdEntries(); count > 0 {
		if name, _, found, err := drivers.IdentifyRoot(d.root); err != nil {
			return nil, err
		} else if !found || name != "icd" {
			return nil, fmt.Errorf("this image has %d partitions in ICD's extended table, which a new loader would overwrite", count)
		}
	}
	bootIndex := d.bootEntry()
	if bootIndex < 0 {
		bootIndex = firstMountable(d.entries)
	}
	if bootIndex < 0 {
		return nil, errors.New("this image has no GEM or BGM partition to boot from")
	}
	entry := d.entries[bootIndex]
	if !entry.Mountable() {
		return nil, fmt.Errorf("the bootable partition has type %q, not GEM or BGM", entry.Type)
	}
	if (int64(entry.Start)+int64(entry.Size))*sectorSize > d.size {
		return nil, errors.New("the boot partition runs past the end of the image")
	}
	v, err := openVolume(d.file, entry.Start)
	if err != nil {
		return nil, err
	}
	sameFAT, err := fatCopiesMatch(v)
	if err != nil {
		return nil, err
	}
	if !sameFAT {
		return nil, errors.New("the boot partition's FAT copies differ, repair the filesystem first")
	}
	result := &InstallResult{}
	if err := replaceDrivers(v, driver, result, now); err != nil {
		return nil, err
	}
	boot, err := installedBootSector(d, entry, driver)
	if err != nil {
		return nil, err
	}
	root := installedRootSector(d, bootIndex, driver)
	if err := v.commit(); err != nil {
		return nil, err
	}
	if _, err := d.file.WriteAt(boot, int64(entry.Start)*sectorSize); err != nil {
		return nil, err
	}
	if _, err := d.file.WriteAt(root, 0); err != nil {
		return nil, err
	}
	return result, nil
}

func firstMountable(entries []Entry) int {
	for i, entry := range entries {
		if entry.Mountable() {
			return i
		}
	}
	return -1
}

// replaceDrivers updates the file set in memory. Stock driver files are
// removed, and a changed copy of the new driver's file is kept as .OLD.
func replaceDrivers(v *volume, driver *drivers.Driver, result *InstallResult, now time.Time) error {
	if offset := v.findEntry(driver.FileName); offset >= 0 {
		data, err := v.readFile(offset)
		if err != nil {
			return fmt.Errorf("%s: %w", driver.FileName, err)
		}
		if stock, err := isStock(driver.FileName, data); err != nil {
			return err
		} else if stock {
			if err := v.removeFile(offset); err != nil {
				return err
			}
		} else {
			renamed := strings.TrimSuffix(driver.FileName, ".SYS") + ".OLD"
			if v.findEntry(renamed) >= 0 {
				return fmt.Errorf("%s differs from the bundled driver and %s already exists", driver.FileName, renamed)
			}
			copy(v.entry(offset)[:11], shortName(renamed))
			result.Renamed = renamed
		}
	}
	for _, item := range drivers.Catalog() {
		if item.FileName == driver.FileName {
			continue
		}
		offset := v.findEntry(item.FileName)
		if offset < 0 {
			continue
		}
		data, err := v.readFile(offset)
		if err != nil {
			continue // leave a damaged file alone
		}
		if stock, err := isStock(item.FileName, data); err != nil {
			return err
		} else if stock {
			if err := v.removeFile(offset); err != nil {
				return err
			}
			result.Removed = append(result.Removed, item.FileName)
		}
	}
	if err := v.addFile(driver.FileName, driver.Binary, now); err != nil {
		return err
	}
	if driver.Name == "cbhd" && v.findEntry(magicLoader) >= 0 {
		result.Warnings = append(result.Warnings, "CBHD's loader will load "+magicLoader+" instead of CBHD.SYS")
	}
	return nil
}

func isStock(fileName string, data []byte) (bool, error) {
	for _, item := range drivers.Catalog() {
		if item.FileName != fileName {
			continue
		}
		stock, err := drivers.Load(item.Name, "acsi")
		if err != nil {
			return false, err
		}
		return bytes.Equal(stock.Binary, data), nil
	}
	return false, nil
}

func fatCopiesMatch(v *volume) (bool, error) {
	size := v.bpb.FATSectors * v.bpb.SectorSize
	first, second := make([]byte, size), make([]byte, size)
	if _, err := v.store.ReadAt(first, v.offset(v.bpb.Reserved)); err != nil {
		return false, err
	}
	if _, err := v.store.ReadAt(second, v.offset(v.bpb.Reserved+v.bpb.FATSectors)); err != nil {
		return false, err
	}
	return bytes.Equal(first, second), nil
}

// installedBootSector keeps the partition's BPB and takes the rest from the loader.
func installedBootSector(d *disk, entry Entry, driver *drivers.Driver) ([]byte, error) {
	old := make([]byte, sectorSize)
	if _, err := d.file.ReadAt(old, int64(entry.Start)*sectorSize); err != nil {
		return nil, err
	}
	sector := append([]byte(nil), driver.Boot...)
	copy(sector[0x0B:0x1E], old[0x0B:0x1E])
	makeExecutable(sector, bootSpareOffset)
	return sector, nil
}

// installedRootSector keeps the partition table and replaces the loader.
func installedRootSector(d *disk, bootIndex int, driver *drivers.Driver) []byte {
	sector := append([]byte(nil), d.root...)
	copy(sector[:rootCodeEnd], driver.Root)
	if binary.BigEndian.Uint32(sector[hdSizeOffset:]) == 0 {
		binary.BigEndian.PutUint32(sector[hdSizeOffset:], uint32(d.size/sectorSize))
	}
	sector[partitionTable+bootIndex*partitionEntry] |= flagExists | flagBootable
	makeExecutable(sector, rootSpareOffset)
	return sector
}
