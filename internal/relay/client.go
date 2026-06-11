package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	RoleSource = "source"
	RoleTarget = "target"
)

// Register sends Relay pairing metadata and waits for the ready byte.
func Register(ctx context.Context, connection net.Conn, sessionID, role string, token []byte, accessToken string) error {
	roleValue := byte(0)
	switch role {
	case RoleSource:
		roleValue = roleSource
	case RoleTarget:
		roleValue = roleTarget
	default:
		return errors.New("Relay role must be source or target")
	}

	stop := applyDeadline(ctx, connection)
	defer stop()
	if err := writeRegistration(connection, sessionID, roleValue, token, accessToken); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("register Relay connection: %w", err)
	}
	ready := []byte{0}
	if _, err := connection.Read(ready); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("wait for Relay peer: %w; possible causes: the peer is not started yet, the same link was started twice on the same side, or the Relay access token does not match", err)
	}
	if ready[0] != 1 {
		return errors.New("Relay returned an invalid ready response")
	}
	return nil
}

func applyDeadline(ctx context.Context, connection net.Conn) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			_ = connection.SetDeadline(time.Now())
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-finished
		_ = connection.SetDeadline(time.Time{})
	}
}
