package image

import "fmt"

const (
	sectorSize = 512
	mebibyte   = 2048 // physical sectors

	// MinSizeMB and MaxSizeMB bound the image size; 1 GB is the ACSI limit.
	MinSizeMB = 4
	MaxSizeMB = 1024

	maxPartitions  = 4 // XGM chains are not supported
	firstPartition = 2 // sector 0 is the root, sector 1 the bad sector list
	badSectorCount = 1
	// SmallBootMB keeps the boot partition GEM, LargeBootMB is the BGM default.
	SmallBootMB = 15
	LargeBootMB = 128
	minBootMB   = 4

	minDataSectors    = 16 * mebibyte // smaller remainders join the boot partition
	gemLimitSectors   = 16 * mebibyte // smaller partitions are GEM, others BGM
	partitionAlign    = 16            // the largest logical sector, 8 KB
	rootEntries       = 256           // fills whole logical sectors up to 8 KB
	maxClusters       = 32766
	maxLogicalCount   = 65535
	reservedSectors   = 1
	fatCount          = 2
	sectorsPerCluster = 2
)

// TOS describes the limits of one TOS version.
type TOS struct {
	Version      string
	MaxPartition uint32 // physical sectors
	MaxLogical   int    // largest logical sector in bytes
	SupportsSCSI bool
}

const (
	limit128 = 128 * mebibyte                                 // cautious, TOS 1.02 is unreliable above this
	limit512 = (5119 * mebibyte / 10) &^ (partitionAlign - 1) // 511.9 MB
)

var tosVersions = []TOS{
	{"1.02", limit128, 4096, false},
	{"1.04", limit512, 8192, false},
	{"1.06", limit512, 8192, false},
	{"1.62", limit512, 8192, false},
	{"2.05", limit512, 8192, false},
	{"2.06", limit512, 8192, false},
	{"3.01", limit512, 8192, true},
	{"3.05", limit512, 8192, true},
	{"3.06", limit512, 8192, true},
}

// TOSVersions lists the supported versions.
func TOSVersions() []TOS {
	return tosVersions
}

// FindTOS looks up a TOS version.
func FindTOS(version string) (TOS, error) {
	for _, tos := range tosVersions {
		if tos.Version == version {
			return tos, nil
		}
	}
	return TOS{}, fmt.Errorf("unsupported TOS version %q", version)
}

// MaxImageMB returns the largest image this TOS version can use in full
// with the given boot partition.
func (t TOS) MaxImageMB(bootMB int) int {
	sectors := uint64(t.bootSectors(bootMB)) + firstPartition + 3*uint64(t.MaxPartition)
	mb := int(sectors / mebibyte)
	if mb > MaxSizeMB {
		return MaxSizeMB
	}
	return mb
}

// MaxBootMB returns the largest boot partition, rounded up to a whole MB.
func (t TOS) MaxBootMB() int {
	return int((t.MaxPartition + mebibyte - 1) / mebibyte)
}

// DefaultBootMB returns the boot partition size used unless another is chosen.
func (t TOS) DefaultBootMB() int {
	if t.PrefersLargeBoot() {
		return LargeBootMB
	}
	return SmallBootMB
}

// PrefersLargeBoot reports whether this TOS starts with a BGM boot partition.
// Older versions keep the small GEM partition, as they may have bugs with
// larger ones.
func (t TOS) PrefersLargeBoot() bool {
	return t.Version >= "3"
}

func (t TOS) bootSectors(bootMB int) uint32 {
	sectors := align(uint32(bootMB) * mebibyte)
	if sectors > t.MaxPartition {
		return t.MaxPartition
	}
	return sectors
}

// Partition is one planned partition.
type Partition struct {
	Start    uint32 // physical sector
	Sectors  uint32 // physical sectors
	Bootable bool
	Geometry Geometry
}

// Type returns the partition ID stored in the root sector.
func (p Partition) Type() string {
	if p.Sectors < gemLimitSectors {
		return "GEM"
	}
	return "BGM"
}

// Plan is the layout of a whole image.
type Plan struct {
	Sectors    uint32
	Partitions []Partition
}

// NewPlan lays out an image: a boot partition, then larger data partitions.
func NewPlan(sizeMB, bootMB int, tos TOS) (*Plan, error) {
	if sizeMB < MinSizeMB || sizeMB > MaxSizeMB {
		return nil, fmt.Errorf("image size must be %d to %d MB", MinSizeMB, MaxSizeMB)
	}
	if bootMB < minBootMB || bootMB > tos.MaxBootMB() {
		return nil, fmt.Errorf("on TOS %s the boot partition must be %d to %d MB", tos.Version, minBootMB, tos.MaxBootMB())
	}
	if sizeMB > tos.MaxImageMB(bootMB) {
		return nil, fmt.Errorf("TOS %s supports images up to %d MB with %d partitions and this boot partition",
			tos.Version, tos.MaxImageMB(bootMB), maxPartitions)
	}
	total := uint32(sizeMB) * mebibyte
	free := total - firstPartition
	boot := tos.bootSectors(bootMB)
	var sizes []uint32
	whole := align(free)
	switch {
	case whole <= tos.MaxPartition && (free < boot || free-boot < minDataSectors):
		sizes = []uint32{whole}
	case free-boot < mebibyte: // too little left over for another partition
		sizes = []uint32{boot}
	default:
		sizes = append([]uint32{boot}, splitData(free-boot, tos.MaxPartition)...)
	}
	plan := &Plan{Sectors: total}
	start := uint32(firstPartition)
	for i, size := range sizes {
		geometry, err := newGeometry(size, tos.MaxLogical)
		if err != nil {
			return nil, err
		}
		plan.Partitions = append(plan.Partitions, Partition{start, size, i == 0, geometry})
		start += size
	}
	return plan, nil
}

// splitData divides free sectors equally into as few partitions as fit.
func splitData(free, limit uint32) []uint32 {
	count := (free + limit - 1) / limit
	each := align(free / count)
	sizes := make([]uint32, count)
	for i := range sizes {
		sizes[i] = each
	}
	return sizes
}

func align(sectors uint32) uint32 {
	return sectors &^ (partitionAlign - 1)
}

// Geometry is the filesystem layout of one partition, in logical sectors
// unless noted otherwise.
type Geometry struct {
	LogicalSize int // bytes
	Logical     int
	FATSectors  int
	RootSectors int
	Clusters    int
}

// DataStart returns the first data sector.
func (g Geometry) DataStart() int {
	return reservedSectors + fatCount*g.FATSectors + g.RootSectors
}

func newGeometry(sectors uint32, maxLogical int) (Geometry, error) {
	for size := sectorSize; size <= maxLogical; size *= 2 {
		logical := int(sectors) / (size / sectorSize)
		if logical > maxLogicalCount {
			continue
		}
		root := rootEntries * 32 / size
		fat := 1
		clusters := 0
		for {
			clusters = (logical - reservedSectors - fatCount*fat - root) / sectorsPerCluster
			if (clusters+2)*2 <= fat*size {
				break
			}
			fat++
		}
		if clusters <= maxClusters {
			return Geometry{size, logical, fat, root, clusters}, nil
		}
	}
	return Geometry{}, fmt.Errorf("no valid filesystem for a partition of %d sectors", sectors)
}
