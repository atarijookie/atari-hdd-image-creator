package image

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	entrySize     = 32
	entryDeleted  = 0xE5
	attrVolume    = 0x08
	attrDirectory = 0x10
	attrArchive   = 0x20
	firstCluster  = 2
	endOfChain    = 0xFFFF
	mediaEntry    = 0xFFF8
	chainEndFrom  = 0xFFF8 // GEMDOS treats these as the end of a chain
)

// storage is what volumes need from an image file.
type storage interface {
	io.ReaderAt
	io.WriterAt
}

// BPB holds the filesystem values from a partition's first sector.
type BPB struct {
	SectorSize     int
	ClusterSectors int
	Reserved       int
	FATs           int
	RootEntries    int
	Sectors        int
	FATSectors     int
}

func parseBPB(sector []byte) BPB {
	le := binary.LittleEndian
	return BPB{
		SectorSize:     int(le.Uint16(sector[0x0B:])),
		ClusterSectors: int(sector[0x0D]),
		Reserved:       int(le.Uint16(sector[0x0E:])),
		FATs:           int(sector[0x10]),
		RootEntries:    int(le.Uint16(sector[0x11:])),
		Sectors:        int(le.Uint16(sector[0x13:])),
		FATSectors:     int(le.Uint16(sector[0x16:])),
	}
}

func (b BPB) rootStart() int { return b.Reserved + b.FATs*b.FATSectors }
func (b BPB) rootSectors() int {
	if b.SectorSize == 0 {
		return 0
	}
	return b.RootEntries * entrySize / b.SectorSize
}
func (b BPB) dataStart() int { return b.rootStart() + b.rootSectors() }

// Clusters returns the number of data clusters.
func (b BPB) Clusters() int {
	if b.ClusterSectors == 0 || b.Sectors < b.dataStart() {
		return 0
	}
	return (b.Sectors - b.dataStart()) / b.ClusterSectors
}

// bootableProblem reports why the loaders could not use this filesystem.
func (b BPB) bootableProblem() string {
	switch {
	case b.SectorSize < sectorSize || b.SectorSize&(b.SectorSize-1) != 0 || b.SectorSize > 8192:
		return fmt.Sprintf("logical sector size %d is not usable", b.SectorSize)
	case b.ClusterSectors != sectorsPerCluster:
		return fmt.Sprintf("%d sectors per cluster, loaders need %d", b.ClusterSectors, sectorsPerCluster)
	case b.FATs != fatCount:
		return fmt.Sprintf("%d FATs, loaders need %d", b.FATs, fatCount)
	case b.Reserved == 0 || b.FATSectors == 0 || b.RootEntries == 0 || b.Clusters() == 0:
		return "filesystem values are empty or invalid"
	case b.RootEntries*entrySize%b.SectorSize != 0:
		return "root directory does not fill whole logical sectors"
	case b.Clusters() > maxClusters:
		return fmt.Sprintf("%d clusters is more than TOS can use", b.Clusters())
	case (b.Clusters()+firstCluster)*2 > b.FATSectors*b.SectorSize:
		return "FAT is too small for its clusters"
	}
	return ""
}

// volume is a partition's filesystem, edited in memory until commit.
type volume struct {
	store storage
	base  int64 // byte offset of the partition
	bpb   BPB
	fat   []uint16
	root  []byte
}

// openVolume reads a partition's FAT and root directory.
func openVolume(store storage, startSector uint32) (*volume, error) {
	base := int64(startSector) * sectorSize
	sector := make([]byte, sectorSize)
	if _, err := store.ReadAt(sector, base); err != nil {
		return nil, err
	}
	v := &volume{store: store, base: base, bpb: parseBPB(sector)}
	if problem := v.bpb.bootableProblem(); problem != "" {
		return nil, fmt.Errorf("partition at sector %d: %s", startSector, problem)
	}
	data := make([]byte, v.bpb.FATSectors*v.bpb.SectorSize)
	if _, err := store.ReadAt(data, v.offset(v.bpb.Reserved)); err != nil {
		return nil, err
	}
	v.fat = make([]uint16, v.bpb.Clusters()+firstCluster)
	for i := range v.fat {
		v.fat[i] = binary.LittleEndian.Uint16(data[2*i:])
	}
	v.root = make([]byte, v.bpb.RootEntries*entrySize)
	if _, err := store.ReadAt(v.root, v.offset(v.bpb.rootStart())); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *volume) offset(sector int) int64 {
	return v.base + int64(sector)*int64(v.bpb.SectorSize)
}

func (v *volume) clusterBytes() int {
	return v.bpb.ClusterSectors * v.bpb.SectorSize
}

func (v *volume) clusterOffset(cluster uint16) int64 {
	return v.offset(v.bpb.dataStart() + (int(cluster)-firstCluster)*v.bpb.ClusterSectors)
}

// findEntry returns the root offset of a file, or -1.
func (v *volume) findEntry(name string) int {
	short := []byte(shortName(name))
	for offset := 0; offset < len(v.root); offset += entrySize {
		entry := v.root[offset : offset+entrySize]
		if entry[0] == 0 {
			break
		}
		if entry[0] != entryDeleted && entry[11]&attrVolume == 0 && bytes.Equal(entry[:11], short) {
			return offset
		}
	}
	return -1
}

func (v *volume) entry(offset int) []byte {
	return v.root[offset : offset+entrySize]
}

// chain lists a file's clusters, in order.
func (v *volume) chain(offset int) ([]uint16, error) {
	entry := v.entry(offset)
	cluster := binary.LittleEndian.Uint16(entry[26:])
	var clusters []uint16
	for cluster != 0 && cluster < chainEndFrom {
		if int(cluster) < firstCluster || int(cluster) >= len(v.fat) || len(clusters) >= len(v.fat) {
			return nil, fmt.Errorf("broken FAT chain at cluster %d", cluster)
		}
		clusters = append(clusters, cluster)
		cluster = v.fat[cluster]
	}
	return clusters, nil
}

// readFile returns a file's contents.
func (v *volume) readFile(offset int) ([]byte, error) {
	size := int(binary.LittleEndian.Uint32(v.entry(offset)[28:]))
	clusters, err := v.chain(offset)
	if err != nil {
		return nil, err
	}
	if size > len(clusters)*v.clusterBytes() {
		return nil, fmt.Errorf("file is larger than its %d clusters", len(clusters))
	}
	data := make([]byte, len(clusters)*v.clusterBytes())
	for i, cluster := range clusters {
		chunk := data[i*v.clusterBytes() : (i+1)*v.clusterBytes()]
		if _, err := v.store.ReadAt(chunk, v.clusterOffset(cluster)); err != nil {
			return nil, err
		}
	}
	return data[:size], nil
}

// removeFile frees a file's clusters and marks its entry deleted.
func (v *volume) removeFile(offset int) error {
	clusters, err := v.chain(offset)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		v.fat[cluster] = 0
	}
	v.root[offset] = entryDeleted
	return nil
}

func (v *volume) freeClusters() int {
	free := 0
	for _, value := range v.fat[firstCluster:] {
		if value == 0 {
			free++
		}
	}
	return free
}

// freeEntry returns the first root entry that may be used. Stopping at the
// first unused entry keeps the file where ICD's loader will find it.
func (v *volume) freeEntry() int {
	for offset := 0; offset < len(v.root); offset += entrySize {
		if v.root[offset] == 0 || v.root[offset] == entryDeleted {
			return offset
		}
	}
	return -1
}

// addFile allocates clusters and writes a file's data. The FAT and directory
// reach the disk on commit.
func (v *volume) addFile(name string, data []byte, now time.Time) error {
	count := (len(data) + v.clusterBytes() - 1) / v.clusterBytes()
	if count > v.freeClusters() {
		return fmt.Errorf("%s needs %d clusters, the boot partition has %d free", name, count, v.freeClusters())
	}
	offset := v.freeEntry()
	if offset < 0 {
		return fmt.Errorf("the boot partition's root directory is full")
	}
	clusters := make([]uint16, 0, count)
	for cluster := firstCluster; len(clusters) < count; cluster++ {
		if v.fat[cluster] == 0 {
			clusters = append(clusters, uint16(cluster))
		}
	}
	for i, cluster := range clusters {
		start := i * v.clusterBytes()
		end := start + v.clusterBytes()
		if end > len(data) {
			end = len(data)
		}
		if _, err := v.store.WriteAt(data[start:end], v.clusterOffset(cluster)); err != nil {
			return err
		}
		if i+1 < len(clusters) {
			v.fat[cluster] = clusters[i+1]
		} else {
			v.fat[cluster] = endOfChain
		}
	}
	entry := directoryEntry(name, len(data), now)
	if count > 0 {
		binary.LittleEndian.PutUint16(entry[26:], clusters[0])
	}
	copy(v.root[offset:], entry)
	return nil
}

// commit writes both FAT copies and the root directory.
func (v *volume) commit() error {
	data := make([]byte, v.bpb.FATSectors*v.bpb.SectorSize)
	for i, value := range v.fat {
		binary.LittleEndian.PutUint16(data[2*i:], value)
	}
	for index := 0; index < v.bpb.FATs; index++ {
		sector := v.bpb.Reserved + index*v.bpb.FATSectors
		if _, err := v.store.WriteAt(data, v.offset(sector)); err != nil {
			return err
		}
	}
	_, err := v.store.WriteAt(v.root, v.offset(v.bpb.rootStart()))
	return err
}
