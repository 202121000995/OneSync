package network

import (
	"context"
	"net"
	"sync"
	"time"
)

// Session sends and receives framed messages over one connection.
type Session interface {
	Send(ctx context.Context, message Message) error
	Receive(ctx context.Context) (Message, error)
	Close() error
}

type session struct {
	connection net.Conn
	codec      *Codec
	sendMu     sync.Mutex
	receiveMu  sync.Mutex
}

// NewSession wraps a connected stream in a framed protocol session.
func NewSession(connection net.Conn, maxPayload uint32) (Session, error) {
	codec, err := NewCodec(maxPayload)
	if err != nil {
		return nil, err
	}
	return &session{connection: connection, codec: codec}, nil
}

func (s *session) Send(ctx context.Context, message Message) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	stop := applyContextDeadline(ctx, s.connection.SetWriteDeadline)
	defer stop()
	err := s.codec.Write(s.connection, message)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (s *session) Receive(ctx context.Context) (Message, error) {
	s.receiveMu.Lock()
	defer s.receiveMu.Unlock()

	stop := applyContextDeadline(ctx, s.connection.SetReadDeadline)
	defer stop()
	message, err := s.codec.Read(s.connection)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Message{}, ctxErr
	}
	return message, err
}

func (s *session) Close() error {
	return s.connection.Close()
}

func applyContextDeadline(ctx context.Context, setDeadline func(time.Time) error) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = setDeadline(deadline)
	}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now())
		case <-done:
		}
	}()

	return func() {
		close(done)
		<-finished
		_ = setDeadline(time.Time{})
	}
}
