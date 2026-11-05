// Package image builds, inspects and updates Atari hard disk images.
package image

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atarijookie/atari-hdd-image-creator/internal/drivers"
)

// Options describes an image to create.
type Options struct {
	Path   string
	Plan   *Plan
	Driver *drivers.Driver
	Force  bool
	Now    time.Time
}

// Create writes a new image. A failed attempt leaves no file behind.
func Create(options Options) (err error) {
	flags := os.O_RDWR | os.O_CREATE | os.O_EXCL
	if options.Force {
		flags = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(options.Path, flags, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(options.Path)
		}
	}()
	return write(file, options)
}

func write(file *os.File, options Options) error {
	plan, driver := options.Plan, options.Driver
	if err := file.Truncate(int64(plan.Sectors) * sectorSize); err != nil {
		return err
	}
	if _, err := file.WriteAt(rootSector(driver.Root, plan), 0); err != nil {
		return err
	}
	if _, err := file.WriteAt(badSectorList(), sectorSize); err != nil {
		return err
	}
	for _, partition := range plan.Partitions {
		var loader []byte
		if partition.Bootable {
			loader = driver.Boot
		}
		if _, err := file.WriteAt(bootSector(loader, partition.Geometry), int64(partition.Start)*sectorSize); err != nil {
			return err
		}
		v, err := openVolume(file, partition.Start)
		if err != nil {
			return err
		}
		v.fat[0], v.fat[1] = mediaEntry, endOfChain
		if partition.Bootable {
			if err := v.addFile(driver.FileName, driver.Binary, options.Now); err != nil {
				return err
			}
		}
		if err := v.commit(); err != nil {
			return err
		}
	}
	return nil
}

func directoryEntry(name string, size int, now time.Time) []byte {
	entry := make([]byte, entrySize)
	copy(entry[:11], shortName(name))
	entry[11] = attrArchive
	le := binary.LittleEndian
	le.PutUint16(entry[22:], uint16(now.Hour()<<11|now.Minute()<<5|now.Second()/2))
	le.PutUint16(entry[24:], uint16((now.Year()-1980)<<9|int(now.Month())<<5|now.Day()))
	le.PutUint32(entry[28:], uint32(size))
	return entry
}

// shortName converts NAME.EXT to the 11-character directory form.
func shortName(name string) string {
	base, extension := name, ""
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		base, extension = name[:dot], name[dot+1:]
	}
	return fmt.Sprintf("%-8s%-3s", base, extension)
}
