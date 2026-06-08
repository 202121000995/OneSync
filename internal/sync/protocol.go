package sync

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/scanner"
)

func encodeSnapshot(snapshot scanner.Snapshot) ([]byte, error) {
	return json.Marshal(snapshot)
}

func decodeSnapshot(payload []byte) (scanner.Snapshot, error) {
	var snapshot scanner.Snapshot
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return scanner.Snapshot{}, err
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return scanner.Snapshot{}, err
	}
	if snapshot.Files == nil {
		snapshot.Files = make(map[string]scanner.FileEntry)
	}
	if err := validateSnapshot("remote", snapshot); err != nil {
		return scanner.Snapshot{}, err
	}
	if len(snapshot.Files) > MaxOperations {
		return scanner.Snapshot{}, fmt.Errorf("snapshot contains %d files, limit is %d", len(snapshot.Files), MaxOperations)
	}
	return snapshot, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("snapshot payload contains multiple JSON values")
}

func sendPlan(ctx context.Context, session network.Session, requestID uint64, count int) error {
	if count < 0 || count > MaxOperations {
		return errors.New("sync plan operation count is invalid")
	}
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(count))
	return session.Send(ctx, network.Message{
		Type: network.MessageSyncPlan, RequestID: requestID, Payload: payload,
	})
}

func decodePlan(payload []byte) (int, error) {
	if len(payload) != 4 {
		return 0, errors.New("sync plan payload must contain 4 bytes")
	}
	count := binary.BigEndian.Uint32(payload)
	if count > MaxOperations {
		return 0, fmt.Errorf("sync plan contains %d operations, limit is %d", count, MaxOperations)
	}
	return int(count), nil
}

func expectAck(ctx context.Context, session network.Session, requestID uint64) error {
	message, err := session.Receive(ctx)
	if err != nil {
		return err
	}
	if message.Type != network.MessageAck || message.RequestID != requestID {
		return errors.New("unexpected acknowledgement")
	}
	return nil
}
