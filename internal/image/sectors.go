package image

import "encoding/binary"

const (
	hdSizeOffset    = 0x1C2
	partitionTable  = 0x1C6
	partitionEntry  = 12
	badStartOffset  = 0x1F6
	badCountOffset  = 0x1FA
	checksumOffset  = 0x1FE
	rootSpareOffset = 0x1C0 // free in every root loader
	bootSpareOffset = 0x1FC // free in every boot loader

	bootableMagic = 0x1234
	dosSignature  = 0x55AA // AHDI would read such a root sector as DOS format

	flagExists   = 0x01
	flagBootable = 0x80

	rootCodeEnd = hdSizeOffset // loaders never run past this

	// ICD partitioning keeps 8 more entries here, over part of the loader area.
	icdTable      = 0x156
	icdTableCount = 8
)

func sectorSum(sector []byte) uint16 {
	var sum uint16
	for i := 0; i < sectorSize; i += 2 {
		sum += binary.BigEndian.Uint16(sector[i:])
	}
	return sum
}

// makeExecutable sets the checksum word, avoiding the DOS signature.
func makeExecutable(sector []byte, spare int) {
	binary.BigEndian.PutUint16(sector[checksumOffset:], 0)
	check := bootableMagic - sectorSum(sector)
	if check == dosSignature {
		sector[spare+1]++
		check = bootableMagic - sectorSum(sector)
	}
	binary.BigEndian.PutUint16(sector[checksumOffset:], check)
}

// rootSector builds sector 0 from a root loader and the plan.
func rootSector(loader []byte, plan *Plan) []byte {
	sector := append([]byte(nil), loader...)
	binary.BigEndian.PutUint32(sector[hdSizeOffset:], plan.Sectors)
	for i, partition := range plan.Partitions {
		entry := sector[partitionTable+i*partitionEntry:]
		entry[0] = flagExists
		if partition.Bootable {
			entry[0] |= flagBootable
		}
		copy(entry[1:4], partition.Type())
		binary.BigEndian.PutUint32(entry[4:], partition.Start)
		binary.BigEndian.PutUint32(entry[8:], partition.Sectors)
	}
	binary.BigEndian.PutUint32(sector[badStartOffset:], 1)
	binary.BigEndian.PutUint32(sector[badCountOffset:], badSectorCount)
	makeExecutable(sector, rootSpareOffset)
	return sector
}

// badSectorList builds an empty list: count 0, then the 0xA5 checksum.
func badSectorList() []byte {
	sector := make([]byte, sectorSize)
	sector[5] = 0xA5
	return sector
}

// bootSector builds a partition's first sector. A nil loader makes it
// non-executable.
func bootSector(loader []byte, g Geometry) []byte {
	sector := make([]byte, sectorSize)
	copy(sector, loader)
	le := binary.LittleEndian
	le.PutUint16(sector[0x0B:], uint16(g.LogicalSize))
	sector[0x0D] = sectorsPerCluster
	le.PutUint16(sector[0x0E:], reservedSectors)
	sector[0x10] = fatCount
	le.PutUint16(sector[0x11:], rootEntries)
	le.PutUint16(sector[0x13:], uint16(g.Logical))
	sector[0x15] = 0xF8
	le.PutUint16(sector[0x16:], uint16(g.FATSectors))
	le.PutUint16(sector[0x18:], 32)
	le.PutUint16(sector[0x1A:], 64)
	if loader != nil {
		makeExecutable(sector, bootSpareOffset)
		return sector
	}
	if sectorSum(sector) == bootableMagic {
		sector[checksumOffset]++
	}
	return sector
}
