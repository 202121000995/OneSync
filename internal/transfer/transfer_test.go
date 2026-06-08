package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/202121000995/OneSync/internal/network"
)

func TestFileTransferCreatesLargeFile(t *testing.T) {
	content := bytes.Repeat([]byte("OneSync transfer block\n"), 50000)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "source.bin")
	writeTestFile(t, sourcePath, content)

	runTransfer(t, sourcePath, "nested/target.bin", targetRoot, 32<<10)

	got, err := os.ReadFile(filepath.Join(targetRoot, "nested", "target.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("received %d bytes, want %d matching bytes", len(got), len(content))
	}
}

func TestFileTransferReplacesExistingFile(t *testing.T) {
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "source.txt")
	targetPath := filepath.Join(targetRoot, "file.txt")
	writeTestFile(t, sourcePath, []byte("new content"))
	writeTestFile(t, targetPath, []byte("old content"))

	runTransfer(t, sourcePath, "file.txt", targetRoot, 4)

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "new content" {
		t.Fatalf("target content = %q, want new content", got)
	}
}

func TestFileTransferResumesFromExistingPart(t *testing.T) {
	content := bytes.Repeat([]byte("resume-data-"), 10000)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "source.bin")
	writeTestFile(t, sourcePath, content)

	_, hash, err := inspectFile(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("inspectFile() error = %v", err)
	}
	relativePath := "resume/file.bin"
	fileID := makeFileID(relativePath, hash)
	partDir := filepath.Join(targetRoot, ".onesync-part")
	if err := os.MkdirAll(partDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	partPath := filepath.Join(partDir, fmt.Sprintf("%x.part", fileID))
	resumeOffset := len(content) / 3
	writeTestFile(t, partPath, content[:resumeOffset])

	runTransfer(t, sourcePath, relativePath, targetRoot, 4096)

	got, err := os.ReadFile(filepath.Join(targetRoot, "resume", "file.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed target content does not match source")
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("temporary part still exists, Stat() error = %v", err)
	}
}

func TestFileTransferResumesAfterInterruptedSession(t *testing.T) {
	content := bytes.Repeat([]byte("disconnect-resume-"), 20000)
	sourcePath := filepath.Join(t.TempDir(), "source.bin")
	targetRoot := t.TempDir()
	writeTestFile(t, sourcePath, content)

	client, server := transferSessionPair(t)
	receiverErrors := make(chan error, 1)
	go func() {
		receiverErrors <- (Receiver{Root: targetRoot}).ReceiveFile(context.Background(), server)
	}()
	interrupted := &interruptingSession{Session: client, chunksBeforeFailure: 2}
	if err := (Sender{ChunkSize: 4096}).SendFile(
		context.Background(), interrupted, 88, sourcePath, "resume.bin",
	); err == nil {
		t.Fatal("first SendFile() error = nil, want interrupted transfer")
	}
	if err := <-receiverErrors; err == nil {
		t.Fatal("first ReceiveFile() error = nil, want interrupted transfer")
	}

	runTransfer(t, sourcePath, "resume.bin", targetRoot, 4096)
	got, err := os.ReadFile(filepath.Join(targetRoot, "resume.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed file does not match source")
	}
}

func TestFileTransferSupportsEmptyFile(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "empty")
	writeTestFile(t, sourcePath, nil)
	targetRoot := t.TempDir()

	runTransfer(t, sourcePath, "empty", targetRoot, MaxChunkSize)

	info, err := os.Stat(filepath.Join(targetRoot, "empty"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty target size = %d, want 0", info.Size())
	}
}

func TestTargetPathRejectsUnsafePaths(t *testing.T) {
	for _, filePath := range []string{
		"",
		".",
		"../escape",
		"/absolute",
		`C:/drive`,
		`a\b`,
		"a/../b",
	} {
		t.Run(filePath, func(t *testing.T) {
			if _, err := targetPath(t.TempDir(), filePath); err == nil {
				t.Fatalf("targetPath() accepted %q", filePath)
			}
		})
	}
}

func TestPrepareTargetParentRejectsSymbolicLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation commonly requires elevated Windows privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := prepareTargetParent(root, "linked/file.txt"); err == nil {
		t.Fatal("prepareTargetParent() error = nil, want symbolic link rejection")
	}
}

func TestPreparePartDirRejectsSymbolicLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation commonly requires elevated Windows privileges")
	}
	root := t.TempDir()
	partDir := filepath.Join(root, ".onesync-part")
	if err := os.Symlink(t.TempDir(), partDir); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := preparePartDir(partDir); err == nil {
		t.Fatal("preparePartDir() error = nil, want symbolic link rejection")
	}
}

func TestReceiverRejectsWrongChunkOffset(t *testing.T) {
	client, server := transferSessionPair(t)
	targetRoot := t.TempDir()
	content := []byte("content")
	hash := sha256.Sum256(content)
	fileID := sha256.Sum256(append([]byte("file.txt"), hash[:]...))
	beginPayload, err := encodeBegin(fileBegin{
		Path: "file.txt", Size: int64(len(content)), Hash: hash, FileID: fileID,
	})
	if err != nil {
		t.Fatalf("encodeBegin() error = %v", err)
	}

	receiverErrors := make(chan error, 1)
	go func() {
		receiverErrors <- (Receiver{Root: targetRoot}).ReceiveFile(context.Background(), server)
	}()
	if err := client.Send(context.Background(), network.Message{
		Type: network.MessageFileBegin, RequestID: 1, Payload: beginPayload,
	}); err != nil {
		t.Fatalf("Send(begin) error = %v", err)
	}
	if _, err := client.Receive(context.Background()); err != nil {
		t.Fatalf("Receive(begin ack) error = %v", err)
	}
	chunkPayload, err := encodeChunk(1, content)
	if err != nil {
		t.Fatalf("encodeChunk() error = %v", err)
	}
	if err := client.Send(context.Background(), network.Message{
		Type: network.MessageFileChunk, RequestID: 1, Payload: chunkPayload,
	}); err != nil {
		t.Fatalf("Send(chunk) error = %v", err)
	}
	response, err := client.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive(rejection) error = %v", err)
	}
	if response.Type != network.MessageError {
		t.Fatalf("rejection type = %v, want MessageError", response.Type)
	}
	if err := <-receiverErrors; err == nil {
		t.Fatal("ReceiveFile() error = nil, want invalid offset error")
	}
}

func TestReceiverRejectsForgedFileID(t *testing.T) {
	client, server := transferSessionPair(t)
	targetRoot := t.TempDir()
	beginPayload, err := encodeBegin(fileBegin{
		Path: "file.txt", Size: 1, Hash: sha256.Sum256([]byte("x")),
	})
	if err != nil {
		t.Fatalf("encodeBegin() error = %v", err)
	}

	receiverErrors := make(chan error, 1)
	go func() {
		receiverErrors <- (Receiver{Root: targetRoot}).ReceiveFile(context.Background(), server)
	}()
	if err := client.Send(context.Background(), network.Message{
		Type: network.MessageFileBegin, RequestID: 1, Payload: beginPayload,
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	response, err := client.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if response.Type != network.MessageError {
		t.Fatalf("response type = %v, want MessageError", response.Type)
	}
	if err := <-receiverErrors; err == nil {
		t.Fatal("ReceiveFile() error = nil, want forged ID error")
	}
}

func TestReceiverRejectsInvalidRoot(t *testing.T) {
	client, server := transferSessionPair(t)
	_ = client
	if err := (Receiver{}).ReceiveFile(context.Background(), server); err == nil {
		t.Fatal("ReceiveFile() error = nil, want invalid root error")
	}
}

func TestProtocolRejectsOversizedChunk(t *testing.T) {
	payload := make([]byte, 8+MaxChunkSize+1)
	if _, _, err := decodeChunk(payload); err == nil {
		t.Fatal("decodeChunk() accepted an oversized chunk")
	}
}

func runTransfer(t *testing.T, sourcePath, relativePath, targetRoot string, chunkSize int) {
	t.Helper()
	client, server := transferSessionPair(t)
	receiverErrors := make(chan error, 1)
	go func() {
		receiverErrors <- (Receiver{Root: targetRoot}).ReceiveFile(context.Background(), server)
	}()

	if err := (Sender{ChunkSize: chunkSize}).SendFile(
		context.Background(), client, 77, sourcePath, relativePath,
	); err != nil {
		t.Fatalf("SendFile() error = %v", err)
	}
	if err := <-receiverErrors; err != nil {
		t.Fatalf("ReceiveFile() error = %v", err)
	}
}

func transferSessionPair(t *testing.T) (network.Session, network.Session) {
	t.Helper()
	leftConnection, rightConnection := net.Pipe()
	left, err := network.NewSession(leftConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(left) error = %v", err)
	}
	right, err := network.NewSession(rightConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(right) error = %v", err)
	}
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	return left, right
}

func writeTestFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

type interruptingSession struct {
	network.Session
	chunksBeforeFailure int
	chunksSent          int
}

func (s *interruptingSession) Send(ctx context.Context, message network.Message) error {
	if message.Type == network.MessageFileChunk {
		if s.chunksSent >= s.chunksBeforeFailure {
			_ = s.Session.Close()
			return errors.New("simulated connection loss")
		}
		s.chunksSent++
	}
	return s.Session.Send(ctx, message)
}
