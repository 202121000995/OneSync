package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/202121000995/OneSync/internal/network"
)

// Sender sends files in bounded chunks over an authenticated session.
type Sender struct {
	ChunkSize int
}

// SendFile sends one file and resumes from the receiver-confirmed offset.
func (s Sender) SendFile(ctx context.Context, session network.Session, requestID uint64, sourcePath, relativePath string) error {
	if err := validateRelativePath(relativePath); err != nil {
		return fmt.Errorf("validate transfer path: %w", err)
	}
	chunkSize := s.ChunkSize
	if chunkSize == 0 {
		chunkSize = MaxChunkSize
	}
	if chunkSize < 1 || chunkSize > MaxChunkSize {
		return fmt.Errorf("chunk size must be between 1 and %d", MaxChunkSize)
	}

	file, size, hash, err := openSourceFile(ctx, sourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	fileID := makeFileID(relativePath, hash)
	beginPayload, err := encodeBegin(fileBegin{
		Path:   relativePath,
		Size:   size,
		Hash:   hash,
		FileID: fileID,
	})
	if err != nil {
		return err
	}
	if err := session.Send(ctx, network.Message{
		Type: network.MessageFileBegin, RequestID: requestID, Payload: beginPayload,
	}); err != nil {
		return err
	}
	offset, err := receiveAckOffset(ctx, session, requestID)
	if err != nil {
		return err
	}
	if offset > size {
		return errors.New("receiver resume offset exceeds file size")
	}

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek source file: %w", err)
	}

	buffer := make([]byte, chunkSize)
	for offset < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := size - offset
		readSize := len(buffer)
		if remaining < int64(readSize) {
			readSize = int(remaining)
		}
		count, readErr := io.ReadFull(file, buffer[:readSize])
		if readErr != nil {
			return fmt.Errorf("read source file: %w", readErr)
		}
		payload, err := encodeChunk(offset, buffer[:count])
		if err != nil {
			return err
		}
		if err := session.Send(ctx, network.Message{
			Type: network.MessageFileChunk, RequestID: requestID, Payload: payload,
		}); err != nil {
			return err
		}
		offset += int64(count)
		confirmed, err := receiveAckOffset(ctx, session, requestID)
		if err != nil {
			return err
		}
		if confirmed != offset {
			return fmt.Errorf("receiver confirmed offset %d, want %d", confirmed, offset)
		}
	}

	endPayload, err := encodeEnd(size, hash)
	if err != nil {
		return err
	}
	if err := session.Send(ctx, network.Message{
		Type: network.MessageFileEnd, RequestID: requestID, Payload: endPayload,
	}); err != nil {
		return err
	}
	response, err := session.Receive(ctx)
	if err != nil {
		return err
	}
	if response.RequestID != requestID || response.Type != network.MessageAck {
		return errors.New("receiver rejected completed file")
	}
	return nil
}

func receiveAckOffset(ctx context.Context, session network.Session, requestID uint64) (int64, error) {
	response, err := session.Receive(ctx)
	if err != nil {
		return 0, err
	}
	if response.RequestID != requestID || response.Type != network.MessageAck {
		return 0, errors.New("receiver rejected file transfer")
	}
	return decodeOffset(response.Payload)
}

func inspectFile(ctx context.Context, sourcePath string) (int64, [hashSize]byte, error) {
	file, size, hash, err := openSourceFile(ctx, sourcePath)
	if file != nil {
		_ = file.Close()
	}
	return size, hash, err
}

func openSourceFile(ctx context.Context, sourcePath string) (*os.File, int64, [hashSize]byte, error) {
	pathInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, 0, [hashSize]byte{}, fmt.Errorf("inspect source file: %w", err)
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, 0, [hashSize]byte{}, errors.New("source is not a regular file")
	}

	file, err := os.Open(sourcePath)
	if err != nil {
		return nil, 0, [hashSize]byte{}, fmt.Errorf("open source file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, [hashSize]byte{}, fmt.Errorf("stat source file: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) {
		file.Close()
		return nil, 0, [hashSize]byte{}, errors.New("source file changed while opening")
	}

	hash := sha256.New()
	buffer := make([]byte, MaxChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, 0, [hashSize]byte{}, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			file.Close()
			return nil, 0, [hashSize]byte{}, fmt.Errorf("hash source file: %w", readErr)
		}
	}
	var sum [hashSize]byte
	copy(sum[:], hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, 0, [hashSize]byte{}, fmt.Errorf("rewind source file: %w", err)
	}
	return file, info.Size(), sum, nil
}
