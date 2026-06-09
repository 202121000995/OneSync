package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/scanner"
)

type planPayload struct {
	OperationCount int    `json:"operation_count"`
	StandardFiles  uint64 `json:"standard_files"`
	StandardBytes  uint64 `json:"standard_bytes"`
}

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

func sendPlan(ctx context.Context, session network.Session, requestID uint64, plan planPayload) error {
	if plan.OperationCount < 0 || plan.OperationCount > MaxOperations {
		return errors.New("sync plan operation count is invalid")
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode sync plan: %w", err)
	}
	return session.Send(ctx, network.Message{
		Type: network.MessageSyncPlan, RequestID: requestID, Payload: payload,
	})
}

func decodePlan(payload []byte) (planPayload, error) {
	var plan planPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return planPayload{}, fmt.Errorf("decode sync plan: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return planPayload{}, err
	}
	if plan.OperationCount < 0 || plan.OperationCount > MaxOperations {
		return planPayload{}, fmt.Errorf("sync plan contains %d operations, limit is %d", plan.OperationCount, MaxOperations)
	}
	return plan, nil
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
