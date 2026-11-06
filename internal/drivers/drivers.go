// Package drivers reads the embedded driver archive.
package drivers

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/atarijookie/atari-hdd-image-creator/assets"
)

// Driver holds everything needed to make a partition boot one driver.
type Driver struct {
	Name     string
	FileName string // 8.3 name the boot loader searches for
	Root     []byte // 512-byte root sector template
	Boot     []byte // 512-byte boot sector template
	Binary   []byte
}

// Info describes a catalog entry for display.
type Info struct {
	Name, Title, Version, FileName, Licence, Note string
}

type entry struct {
	info Info
	root map[string]string
	boot string
}

var catalog = map[string]entry{
	"ahdi": {
		Info{"ahdi", "Atari AHDI", "6.05c with boot delay fix (reports v6.00)", "SHDRIVER.SYS", "Atari, status unclear",
			"Works in Hatari; finds no drives on Cosmo2EX over ACSI"},
		sameRoot("ahdi_root"), "ahdi_boot",
	},
	"cbhd": {
		Info{"cbhd", "CBHD", "4.57", "CBHD.SYS", "Freeware, unchanged copies allowed",
			"Boots on real ST and TT over ACSI"},
		sameRoot("cbhd_root"), "cbhd_boot",
	},
	"icd": {
		Info{"icd", "ICD PRO", "6.5.5", "ICDBOOT.SYS", "ICD Inc, status unclear",
			"Boots on real ST and TT over ACSI; root loader depends on the bus"},
		map[string]string{"acsi": "icd_root_acsi", "scsi": "icd_root_scsi"}, "icd_boot",
	},
}

func sameRoot(name string) map[string]string {
	return map[string]string{"acsi": name, "scsi": name}
}

// Names lists the catalog in display order.
func Names() []string {
	return []string{"ahdi", "cbhd", "icd"}
}

// Catalog lists every driver in display order.
func Catalog() []Info {
	list := make([]Info, 0, len(catalog))
	for _, name := range Names() {
		list = append(list, catalog[name].info)
	}
	return list
}

// Load returns the named driver with the root loader for the given bus.
func Load(name, bus string) (*Driver, error) {
	item, ok := catalog[name]
	if !ok {
		return nil, fmt.Errorf("unknown driver %q, choose one of: %s", name, strings.Join(Names(), ", "))
	}
	root, ok := item.root[bus]
	if !ok {
		return nil, fmt.Errorf("driver %s does not support bus %q", name, bus)
	}
	files, err := archiveFiles()
	if err != nil {
		return nil, err
	}
	driver := &Driver{
		Name:     name,
		FileName: item.info.FileName,
		Root:     files["loaders/"+root+".bin"],
		Boot:     files["loaders/"+item.boot+".bin"],
		Binary:   files["drivers/"+item.info.FileName],
	}
	if len(driver.Root) != 512 || len(driver.Boot) != 512 || len(driver.Binary) == 0 {
		return nil, fmt.Errorf("driver archive is incomplete for %s", name)
	}
	return driver, nil
}

// Sections of a sector that belong to the loader. The rest holds disk data,
// or a word that may be adjusted to reach the checksum.
const (
	rootCodeEnd   = 0x1C0
	bootCodeStart = 0x1E
	bootCodeEnd   = 0x1FC
)

// IdentifyRoot names the driver whose root loader is in sector. The bus is
// empty when the loader works on either bus.
func IdentifyRoot(sector []byte) (name, bus string, found bool, err error) {
	files, err := archiveFiles()
	if err != nil {
		return "", "", false, err
	}
	for _, candidate := range Names() {
		item := catalog[candidate]
		for _, itemBus := range []string{"acsi", "scsi"} {
			loader := files["loaders/"+item.root[itemBus]+".bin"]
			if bytes.Equal(sector[:rootCodeEnd], loader[:rootCodeEnd]) {
				if item.root["acsi"] == item.root["scsi"] {
					itemBus = ""
				}
				return candidate, itemBus, true, nil
			}
		}
	}
	return "", "", false, nil
}

// IdentifyBoot names the driver whose boot loader is in sector.
func IdentifyBoot(sector []byte) (name string, found bool, err error) {
	files, err := archiveFiles()
	if err != nil {
		return "", false, err
	}
	for _, candidate := range Names() {
		loader := files["loaders/"+catalog[candidate].boot+".bin"]
		if bytes.Equal(sector[bootCodeStart:bootCodeEnd], loader[bootCodeStart:bootCodeEnd]) {
			return candidate, true, nil
		}
	}
	return "", false, nil
}

var (
	archiveOnce  sync.Once
	archive      map[string][]byte
	archiveError error
)

func archiveFiles() (map[string][]byte, error) {
	archiveOnce.Do(func() {
		archive, archiveError = readArchive(assets.DriversZip)
	})
	return archive, archiveError
}

// readArchive unpacks the zip and checks each file against its manifest hash.
func readArchive(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("reading driver archive: %w", err)
	}
	files := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file.Name, err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file.Name, err)
		}
		files[file.Name] = content
	}
	var manifest map[string]string
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		return nil, fmt.Errorf("reading driver manifest: %w", err)
	}
	for name, want := range manifest {
		sum := sha256.Sum256(files[name])
		if hex.EncodeToString(sum[:]) != want {
			return nil, fmt.Errorf("driver archive: %s fails its hash check", name)
		}
	}
	return files, nil
}
