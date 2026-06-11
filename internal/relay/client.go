package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	RoleSource = "source"
	RoleTarget = "target"
)

// ControlClient is a long-lived Relay control connection. It keeps this device
// visible on the Relay and receives invitations for short-lived data sessions.
type ControlClient struct {
	connection net.Conn
	sessionID  string
	role       byte
	sendMu     sync.Mutex
	incoming   chan controlMessage
	errors     chan error
	closeOnce  sync.Once
}

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

// JoinControl registers a client on a long-lived Relay control connection.
func JoinControl(ctx context.Context, connection net.Conn, sessionID, role string, token []byte, accessToken string) (*ControlClient, error) {
	roleValue, err := roleValue(role)
	if err != nil {
		return nil, err
	}
	stop := applyDeadline(ctx, connection)
	defer stop()
	if err := writeControlJoin(connection, sessionID, roleValue, token, accessToken); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("join Relay control connection: %w", err)
	}
	if err := readReady(ctx, connection, "wait for Relay control confirmation"); err != nil {
		return nil, err
	}
	client := &ControlClient{
		connection: connection,
		sessionID:  sessionID,
		role:       roleValue,
		incoming:   make(chan controlMessage, 16),
		errors:     make(chan error, 1),
	}
	go client.readLoop()
	return client, nil
}

// RequestSession asks the Relay to invite both devices into a data session.
func (c *ControlClient) RequestSession(ctx context.Context) ([sessionKeySize]byte, error) {
	var key [sessionKeySize]byte
	stop := applyDeadline(ctx, c.connection)
	defer stop()
	if err := c.writeControl(controlMessageRequestSession, nil); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return key, ctxErr
		}
		return key, fmt.Errorf("request Relay data session: %w", err)
	}
	return c.waitInvitation(ctx)
}

// WaitSession waits until the Relay invites this device into a data session.
func (c *ControlClient) WaitSession(ctx context.Context) ([sessionKeySize]byte, error) {
	stop := applyDeadline(ctx, c.connection)
	defer stop()
	return c.waitInvitation(ctx)
}

func (c *ControlClient) waitInvitation(ctx context.Context) ([sessionKeySize]byte, error) {
	var key [sessionKeySize]byte
	for {
		message, err := c.readControl(ctx)
		if err != nil {
			return key, fmt.Errorf("wait for Relay data session invitation: %w", err)
		}
		switch message.messageType {
		case controlMessageInviteSession:
			if len(message.payload) != sessionKeySize {
				return key, errors.New("Relay data session invitation is invalid")
			}
			copy(key[:], message.payload)
			return key, nil
		case controlMessageError:
			return key, fmt.Errorf("Relay refused data session: %s", string(message.payload))
		}
	}
}

// SendWake tells the opposite peer that local files changed and a new sync
// cycle should start over the existing data session.
func (c *ControlClient) SendWake(ctx context.Context) error {
	stop := applyDeadline(ctx, c.connection)
	defer stop()
	return c.writeControl(controlMessageWake, nil)
}

// WaitWake waits for the opposite peer to signal a local file change.
func (c *ControlClient) WaitWake(ctx context.Context) error {
	for {
		message, err := c.readControl(ctx)
		if err != nil {
			return err
		}
		if message.messageType == controlMessageWake {
			return nil
		}
		if message.messageType == controlMessageError {
			return fmt.Errorf("Relay control error: %s", string(message.payload))
		}
	}
}

// JoinSession joins a Relay data session created by a prior control invitation.
func JoinSession(ctx context.Context, connection net.Conn, sessionID, role string, key [sessionKeySize]byte) error {
	roleValue, err := roleValue(role)
	if err != nil {
		return err
	}
	stop := applyDeadline(ctx, connection)
	defer stop()
	if err := writeSessionJoin(connection, sessionID, roleValue, key); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("join Relay data session: %w", err)
	}
	return readReady(ctx, connection, "wait for Relay data session")
}

// Close closes the Relay control connection.
func (c *ControlClient) Close() error {
	if c == nil || c.connection == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		_ = c.connection.Close()
	})
	return nil
}

func (c *ControlClient) readLoop() {
	for {
		message, err := readControlMessage(c.connection)
		if err != nil {
			select {
			case c.errors <- err:
			default:
			}
			return
		}
		if message.messageType == controlMessagePing {
			_ = c.writeControl(controlMessagePong, nil)
			continue
		}
		select {
		case c.incoming <- message:
		default:
		}
	}
}

func (c *ControlClient) readControl(ctx context.Context) (controlMessage, error) {
	select {
	case message := <-c.incoming:
		return message, nil
	case err := <-c.errors:
		return controlMessage{}, err
	case <-ctx.Done():
		return controlMessage{}, ctx.Err()
	}
}

func (c *ControlClient) writeControl(messageType byte, payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return writeControlMessage(c.connection, messageType, payload)
}

func roleValue(role string) (byte, error) {
	switch role {
	case RoleSource:
		return roleSource, nil
	case RoleTarget:
		return roleTarget, nil
	default:
		return 0, errors.New("Relay role must be source or target")
	}
}

func readReady(ctx context.Context, connection net.Conn, action string) error {
	ready := []byte{0}
	if _, err := connection.Read(ready); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%s: %w", action, err)
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
