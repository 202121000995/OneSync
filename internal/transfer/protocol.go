package transfer

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MaxChunkSize = 1 << 20
	hashSize     = 32
	fileIDSize   = 32
)

type fileBegin struct {
	Path   string
	Size   int64
	Hash   [hashSize]byte
	FileID [fileIDSize]byte
}

func makeFileID(filePath string, hash [hashSize]byte) [fileIDSize]byte {
	return sha256.Sum256(append([]byte(filePath), hash[:]...))
}

func encodeBegin(begin fileBegin) ([]byte, error) {
	if len(begin.Path) == 0 || len(begin.Path) > 65535 {
		return nil, errors.New("file path length is invalid")
	}
	payload := make([]byte, 2+len(begin.Path)+8+hashSize+fileIDSize)
	binary.BigEndian.PutUint16(payload[:2], uint16(len(begin.Path)))
	copy(payload[2:], begin.Path)
	offset := 2 + len(begin.Path)
	binary.BigEndian.PutUint64(payload[offset:offset+8], uint64(begin.Size))
	copy(payload[offset+8:offset+8+hashSize], begin.Hash[:])
	copy(payload[offset+8+hashSize:], begin.FileID[:])
	return payload, nil
}

func decodeBegin(payload []byte) (fileBegin, error) {
	if len(payload) < 2 {
		return fileBegin{}, errors.New("file begin payload is truncated")
	}
	pathLength := int(binary.BigEndian.Uint16(payload[:2]))
	wantLength := 2 + pathLength + 8 + hashSize + fileIDSize
	if pathLength == 0 || len(payload) != wantLength {
		return fileBegin{}, errors.New("file begin payload has invalid length")
	}
	offset := 2 + pathLength
	size := binary.BigEndian.Uint64(payload[offset : offset+8])
	if size > uint64(^uint64(0)>>1) {
		return fileBegin{}, errors.New("file size exceeds supported range")
	}
	begin := fileBegin{
		Path: string(payload[2:offset]),
		Size: int64(size),
	}
	copy(begin.Hash[:], payload[offset+8:offset+8+hashSize])
	copy(begin.FileID[:], payload[offset+8+hashSize:])
	return begin, nil
}

func encodeOffset(offset int64) ([]byte, error) {
	if offset < 0 {
		return nil, errors.New("offset cannot be negative")
	}
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(offset))
	return payload, nil
}

func decodeOffset(payload []byte) (int64, error) {
	if len(payload) != 8 {
		return 0, errors.New("offset payload must contain 8 bytes")
	}
	offset := binary.BigEndian.Uint64(payload)
	if offset > uint64(^uint64(0)>>1) {
		return 0, errors.New("offset exceeds supported range")
	}
	return int64(offset), nil
}

func encodeChunk(offset int64, data []byte) ([]byte, error) {
	if offset < 0 {
		return nil, errors.New("chunk offset cannot be negative")
	}
	if len(data) == 0 || len(data) > MaxChunkSize {
		return nil, fmt.Errorf("chunk length must be between 1 and %d", MaxChunkSize)
	}
	payload := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(payload[:8], uint64(offset))
	copy(payload[8:], data)
	return payload, nil
}

func decodeChunk(payload []byte) (int64, []byte, error) {
	if len(payload) <= 8 || len(payload)-8 > MaxChunkSize {
		return 0, nil, errors.New("chunk payload has invalid length")
	}
	offset, err := decodeOffset(payload[:8])
	if err != nil {
		return 0, nil, err
	}
	return offset, payload[8:], nil
}

func encodeEnd(size int64, hash [hashSize]byte) ([]byte, error) {
	if size < 0 {
		return nil, errors.New("file size cannot be negative")
	}
	payload := make([]byte, 8+hashSize)
	binary.BigEndian.PutUint64(payload[:8], uint64(size))
	copy(payload[8:], hash[:])
	return payload, nil
}

func decodeEnd(payload []byte) (int64, [hashSize]byte, error) {
	if len(payload) != 8+hashSize {
		return 0, [hashSize]byte{}, errors.New("file end payload has invalid length")
	}
	size, err := decodeOffset(payload[:8])
	if err != nil {
		return 0, [hashSize]byte{}, err
	}
	var hash [hashSize]byte
	copy(hash[:], payload[8:])
	return size, hash, nil
}
