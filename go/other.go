package jtrpc

import (
	"encoding/binary"
)

func append2(buf []byte, u uint16) []byte {
	return binary.LittleEndian.AppendUint16(buf, u)
}

func place2(buf []byte, u uint16) {
	binary.LittleEndian.PutUint16(buf, u)
}

func get2(buf []byte) uint16 {
	return binary.LittleEndian.Uint16(buf)
}

func append8(buf []byte, u uint64) []byte {
	return binary.LittleEndian.AppendUint64(buf, u)
}

func place8(buf []byte, u uint64) {
	binary.LittleEndian.PutUint64(buf, u)
}

func get8(buf []byte) uint64 {
	return binary.LittleEndian.Uint64(buf)
}
