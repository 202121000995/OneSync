package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/202121000995/OneSync/internal/network"
)

// Receiver stores incoming files beneath one target root.
type Receiver struct {
	Root string
}

// ReceiveFile receives one file transaction from a session.
func (r Receiver) ReceiveFile(ctx context.Context, session network.Session) error {
	root, err := validateRoot(r.Root)
	if err != nil {
		return err
	}
	beginMessage, err := session.Receive(ctx)
	if err != nil {
		return err
	}
	if beginMessage.Type != network.MessageFileBegin {
		return errors.New("expected file begin message")
	}
	begin, err := decodeBegin(beginMessage.Payload)
	if err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, err)
	}
	if begin.FileID != makeFileID(begin.Path, begin.Hash) {
		return r.reject(ctx, session, beginMessage.RequestID, errors.New("file ID does not match path and hash"))
	}
	finalPath, err := targetPath(root, begin.Path)
	if err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, err)
	}
	if err := prepareTargetParent(root, begin.Path); err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, fmt.Errorf("prepare target parent: %w", err))
	}

	partDir := filepath.Join(root, ".onesync-part")
	if err := preparePartDir(partDir); err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, err)
	}
	partPath := filepath.Join(partDir, fmt.Sprintf("%x.part", begin.FileID))
	part, offset, err := openPart(partPath, begin.Size)
	if err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, err)
	}
	defer part.Close()
	available, err := availableSpace(partDir)
	if err != nil {
		return r.reject(ctx, session, beginMessage.RequestID, fmt.Errorf("check available space: %w", err))
	}
	if uint64(begin.Size-offset) > available {
		return r.reject(ctx, session, beginMessage.RequestID, errors.New("insufficient target disk space"))
	}

	if err := sendOffsetAck(ctx, session, beginMessage.RequestID, offset); err != nil {
		return err
	}

	for {
		message, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if message.RequestID != beginMessage.RequestID {
			return r.reject(ctx, session, message.RequestID, errors.New("request ID changed during file transfer"))
		}

		switch message.Type {
		case network.MessageFileChunk:
			chunkOffset, data, err := decodeChunk(message.Payload)
			if err != nil {
				return r.reject(ctx, session, message.RequestID, err)
			}
			if chunkOffset != offset || int64(len(data)) > begin.Size-offset {
				return r.reject(ctx, session, message.RequestID, errors.New("file chunk offset or length is invalid"))
			}
			if _, err := part.Write(data); err != nil {
				return fmt.Errorf("write temporary file: %w", err)
			}
			offset += int64(len(data))
			if err := sendOffsetAck(ctx, session, message.RequestID, offset); err != nil {
				return err
			}

		case network.MessageFileEnd:
			endSize, endHash, err := decodeEnd(message.Payload)
			if err != nil || endSize != begin.Size || endHash != begin.Hash || offset != begin.Size {
				return r.reject(ctx, session, message.RequestID, errors.New("file end metadata does not match transfer"))
			}
			if err := part.Sync(); err != nil {
				return fmt.Errorf("sync temporary file: %w", err)
			}
			if err := part.Close(); err != nil {
				return fmt.Errorf("close temporary file: %w", err)
			}
			actualHash, err := hashPath(ctx, partPath)
			if err != nil {
				return r.reject(ctx, session, message.RequestID, err)
			}
			if actualHash != begin.Hash {
				_ = os.Remove(partPath)
				return r.reject(ctx, session, message.RequestID, errors.New("received file hash does not match"))
			}
			if err := replaceFile(partPath, finalPath); err != nil {
				return fmt.Errorf("replace target file: %w", err)
			}
			return session.Send(ctx, network.Message{
				Type: network.MessageAck, RequestID: message.RequestID,
			})

		default:
			return r.reject(ctx, session, message.RequestID, errors.New("unexpected message during file transfer"))
		}
	}
}

func validateRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("target root is empty")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve target root: %w", err)
	}
	info, err := os.Lstat(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("stat target root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("target root is not a directory")
	}
	return absoluteRoot, nil
}

func openPart(partPath string, totalSize int64) (*os.File, int64, error) {
	info, err := os.Lstat(partPath)
	if err == nil && !info.Mode().IsRegular() {
		return nil, 0, errors.New("temporary transfer path is not a regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("inspect temporary file: %w", err)
	}

	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("open temporary file: %w", err)
	}
	info, err = file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("stat temporary file: %w", err)
	}
	offset := info.Size()
	if offset > totalSize {
		if err := file.Truncate(0); err != nil {
			file.Close()
			return nil, 0, fmt.Errorf("reset temporary file: %w", err)
		}
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("seek temporary file: %w", err)
	}
	return file, offset, nil
}

func preparePartDir(partDir string) error {
	info, err := os.Lstat(partDir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(partDir, 0o700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create transfer directory: %w", err)
		}
		info, err = os.Lstat(partDir)
	}
	if err != nil {
		return fmt.Errorf("inspect transfer directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("transfer directory is a symbolic link or non-directory")
	}
	if err := os.Chmod(partDir, 0o700); err != nil {
		return fmt.Errorf("secure transfer directory: %w", err)
	}
	return nil
}

func sendOffsetAck(ctx context.Context, session network.Session, requestID uint64, offset int64) error {
	payload, err := encodeOffset(offset)
	if err != nil {
		return err
	}
	return session.Send(ctx, network.Message{
		Type: network.MessageAck, RequestID: requestID, Payload: payload,
	})
}

func (r Receiver) reject(ctx context.Context, session network.Session, requestID uint64, cause error) error {
	_ = session.Send(ctx, network.Message{Type: network.MessageError, RequestID: requestID})
	return cause
}

func hashPath(ctx context.Context, filePath string) ([hashSize]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return [hashSize]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, MaxChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return [hashSize]byte{}, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return [hashSize]byte{}, readErr
		}
	}
	var sum [hashSize]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}
