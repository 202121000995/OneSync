package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const (
	DefaultWaitTimeout = 2 * time.Minute
	DefaultIdleTimeout = 5 * time.Minute
	DefaultMaxWaiting  = 1000
	DefaultMaxActive   = 1000
	DefaultMaxBytes    = int64(1 << 40)
)

type waitingPeer struct {
	connection   net.Conn
	registration registration
	ready        chan pairResult
	done         chan struct{}
}

type pairResult struct {
	peer net.Conn
	err  error
}

type pairOutcome struct {
	peer  net.Conn
	owner bool
	done  chan struct{}
}

// Config controls Relay resource limits.
type Config struct {
	WaitTimeout         time.Duration
	IdleTimeout         time.Duration
	MaxWaiting          int
	MaxActive           int
	MaxBytes            int64
	AccessToken         string
	AccessTokenProvider func() []string
	Logger              *slog.Logger
}

// Broker pairs source and target connections and transparently forwards bytes.
type Broker struct {
	mu                  sync.Mutex
	waiting             map[string]*waitingPeer
	waitTimeout         time.Duration
	idleTimeout         time.Duration
	maxWaiting          int
	maxActive           int
	active              int
	connections         int
	maxConns            int
	maxBytes            int64
	accessTokenHash     [sha256.Size]byte
	accessTokenRequired bool
	accessTokenProvider func() []string
	sessionStats        map[string]*sessionStats
	recentSessions      []SessionSnapshot
	totalSourceBytes    uint64
	totalTargetBytes    uint64
	logger              *slog.Logger
}

// NewBroker validates Relay limits and creates a broker.
func NewBroker(config Config) (*Broker, error) {
	if config.WaitTimeout == 0 {
		config.WaitTimeout = DefaultWaitTimeout
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = DefaultIdleTimeout
	}
	if config.MaxWaiting == 0 {
		config.MaxWaiting = DefaultMaxWaiting
	}
	if config.MaxActive == 0 {
		config.MaxActive = DefaultMaxActive
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = DefaultMaxBytes
	}
	maxInt := int(^uint(0) >> 1)
	if config.WaitTimeout <= 0 || config.IdleTimeout <= 0 ||
		config.MaxWaiting < 1 || config.MaxActive < 1 || config.MaxBytes < 1 ||
		config.MaxActive > (maxInt-config.MaxWaiting)/2 {
		return nil, errors.New("Relay limits are invalid")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if len(config.AccessToken) > maxAccessTokenLength {
		return nil, errors.New("Relay access token is too large")
	}
	accessTokenRequired := config.AccessToken != "" || config.AccessTokenProvider != nil
	accessTokenHash := [sha256.Size]byte{}
	if config.AccessToken != "" {
		accessTokenHash = sha256.Sum256([]byte(config.AccessToken))
	}
	return &Broker{
		waiting:             make(map[string]*waitingPeer),
		waitTimeout:         config.WaitTimeout,
		idleTimeout:         config.IdleTimeout,
		maxWaiting:          config.MaxWaiting,
		maxActive:           config.MaxActive,
		maxConns:            config.MaxWaiting + 2*config.MaxActive,
		maxBytes:            config.MaxBytes,
		accessTokenHash:     accessTokenHash,
		accessTokenRequired: accessTokenRequired,
		accessTokenProvider: config.AccessTokenProvider,
		sessionStats:        make(map[string]*sessionStats),
		logger:              config.Logger,
	}, nil
}

// Handle registers one TLS connection, waits for its peer, and relays bytes.
func (b *Broker) Handle(ctx context.Context, connection net.Conn) error {
	defer connection.Close()
	remote := connection.RemoteAddr().String()
	if !b.acquireConnection() {
		b.logger.Warn("Relay connection rejected: connection limit reached", "remote", remote)
		return errors.New("Relay connection limit reached")
	}
	defer b.releaseConnection()
	b.logger.Info("Relay connection accepted", "remote", remote)

	registrationContext, cancelRegistration := context.WithTimeout(ctx, b.waitTimeout)
	stopDeadline := applyDeadline(registrationContext, connection)
	registration, err := readRegistration(connection)
	registrationContextErr := registrationContext.Err()
	stopDeadline()
	cancelRegistration()
	if err != nil {
		b.logger.Warn("Relay registration failed", "remote", remote, "error", err)
		if registrationContextErr != nil {
			return registrationContextErr
		}
		return err
	}
	sessionLabel := sessionDigest(registration.sessionID)
	role := relayRoleLabel(registration.role)
	if !b.authorize(registration) {
		b.logger.Warn("Relay access token rejected", "remote", remote, "session", sessionLabel, "role", role)
		return errors.New("Relay access token is invalid")
	}
	b.logger.Info("Relay registration accepted", "remote", remote, "session", sessionLabel, "role", role, "token_present", registration.accessTokenPresent)

	outcome, err := b.pair(ctx, connection, registration)
	if err != nil {
		b.logger.Warn("Relay pairing failed", "remote", remote, "session", sessionLabel, "role", role, "error", err)
		return err
	}
	if !outcome.owner {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-outcome.done:
			return nil
		}
	}
	defer close(outcome.done)
	defer b.releaseActive()
	defer outcome.peer.Close()
	if err := writeAll(connection, []byte{1}); err != nil {
		return fmt.Errorf("confirm Relay pairing: %w", err)
	}
	if err := writeAll(outcome.peer, []byte{1}); err != nil {
		return fmt.Errorf("confirm Relay peer pairing: %w", err)
	}
	return b.forward(ctx, connection, outcome.peer, registration.sessionID, registration.role)
}

func (b *Broker) authorize(registration registration) bool {
	if !b.accessTokenRequired {
		return true
	}
	if b.accessTokenProvider != nil {
		for _, token := range b.accessTokenProvider() {
			if token == "" || len(token) > maxAccessTokenLength {
				continue
			}
			if registration.accessTokenPresent && sameToken(sha256.Sum256([]byte(token)), registration.accessTokenHash) {
				return true
			}
		}
		return false
	}
	return registration.accessTokenPresent && sameToken(b.accessTokenHash, registration.accessTokenHash)
}

func (b *Broker) pair(ctx context.Context, connection net.Conn, registration registration) (pairOutcome, error) {
	sessionLabel := sessionDigest(registration.sessionID)
	role := relayRoleLabel(registration.role)
	b.mu.Lock()
	if waiting, exists := b.waiting[registration.sessionID]; exists {
		if waiting.registration.role == registration.role {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay session already has this role")
		}
		if !sameToken(waiting.registration.tokenHash, registration.tokenHash) {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay session authentication failed")
		}
		if b.active >= b.maxActive {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay active session limit reached")
		}
		delete(b.waiting, registration.sessionID)
		b.active++
		b.markActiveLocked(registration.sessionID, waiting.registration.role, waiting.connection.RemoteAddr().String(), registration.role, connection.RemoteAddr().String())
		b.mu.Unlock()
		b.logger.Info("Relay peer matched", "session", sessionLabel, "role", role)
		waiting.ready <- pairResult{peer: connection}
		close(waiting.ready)
		return pairOutcome{owner: false, done: waiting.done}, nil
	}
	if len(b.waiting) >= b.maxWaiting {
		b.mu.Unlock()
		return pairOutcome{}, errors.New("Relay waiting session limit reached")
	}
	waiting := &waitingPeer{
		connection:   connection,
		registration: registration,
		ready:        make(chan pairResult, 1),
		done:         make(chan struct{}),
	}
	b.waiting[registration.sessionID] = waiting
	b.markWaitingLocked(registration.sessionID, registration.role, connection.RemoteAddr().String())
	b.mu.Unlock()
	b.logger.Info("Relay peer waiting", "session", sessionLabel, "role", role, "timeout", b.waitTimeout.String())

	timer := time.NewTimer(b.waitTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		b.removeWaiting(registration.sessionID, waiting)
		return pairOutcome{}, ctx.Err()
	case <-timer.C:
		b.removeWaiting(registration.sessionID, waiting)
		b.logger.Warn("Relay pairing timed out", "session", sessionLabel, "role", role)
		return pairOutcome{}, errors.New("Relay pairing timed out")
	case result := <-waiting.ready:
		return pairOutcome{peer: result.peer, owner: true, done: waiting.done}, result.err
	}
}

func (b *Broker) acquireConnection() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connections >= b.maxConns {
		return false
	}
	b.connections++
	return true
}

func (b *Broker) releaseConnection() {
	b.mu.Lock()
	b.connections--
	b.mu.Unlock()
}

func (b *Broker) releaseActive() {
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
}

func (b *Broker) removeWaiting(sessionID string, waiting *waitingPeer) {
	b.mu.Lock()
	if b.waiting[sessionID] == waiting {
		delete(b.waiting, sessionID)
		b.markClosedLocked(sessionID, "waiting removed")
	}
	b.mu.Unlock()
}

func (b *Broker) forward(ctx context.Context, left, right net.Conn, sessionID string, leftRole byte) error {
	sessionLabel := sessionDigest(sessionID)
	b.logger.Info("Relay session paired", "session", sessionLabel)
	defer b.logger.Info("Relay session closed", "session", sessionLabel)

	type copyResult struct {
		err error
	}
	results := make(chan copyResult, 2)
	copyStream := func(destination, source net.Conn, sourceToTarget bool) {
		buffer := make([]byte, 32<<10)
		var transferred int64
		for {
			deadline := time.Now().Add(b.idleTimeout)
			_ = source.SetReadDeadline(deadline)
			_ = destination.SetWriteDeadline(deadline)
			count, readErr := source.Read(buffer)
			if count > 0 {
				if transferred+int64(count) > b.maxBytes {
					results <- copyResult{err: errors.New("Relay byte limit exceeded")}
					return
				}
				if err := writeAll(destination, buffer[:count]); err != nil {
					results <- copyResult{err: err}
					return
				}
				transferred += int64(count)
				b.addSessionBytes(sessionID, sourceToTarget, uint64(count))
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					results <- copyResult{}
				} else {
					results <- copyResult{err: readErr}
				}
				return
			}
		}
	}
	leftToRightIsSourceToTarget := leftRole == roleSource
	go copyStream(right, left, leftToRightIsSourceToTarget)
	go copyStream(left, right, !leftToRightIsSourceToTarget)

	select {
	case <-ctx.Done():
		err := ctx.Err()
		b.markClosed(sessionID, err.Error())
		return err
	case result := <-results:
		_ = left.SetDeadline(time.Now())
		_ = right.SetDeadline(time.Now())
		reason := "closed"
		if result.err != nil {
			reason = result.err.Error()
		}
		b.markClosed(sessionID, reason)
		return result.err
	}
}

// Snapshot returns a point-in-time Relay runtime view.
func (b *Broker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	sessions := make([]SessionSnapshot, 0, len(b.sessionStats)+len(b.recentSessions))
	for _, session := range b.sessionStats {
		sessions = append(sessions, snapshotFromStats(session))
	}
	sessions = append(sessions, b.recentSessions...)
	return Snapshot{
		Connections:      b.connections,
		Waiting:          len(b.waiting),
		Active:           b.active,
		TotalSourceBytes: b.totalSourceBytes,
		TotalTargetBytes: b.totalTargetBytes,
		Sessions:         sessions,
	}
}

func (b *Broker) markWaitingLocked(sessionID string, role byte, remote string) {
	now := time.Now().UTC()
	stats := &sessionStats{
		sessionID: sessionDigest(sessionID),
		state:     "waiting",
		startedAt: now,
		updatedAt: now,
	}
	if role == roleSource {
		stats.sourceRemote = remote
	} else {
		stats.targetRemote = remote
	}
	b.sessionStats[sessionID] = stats
}

func (b *Broker) markActiveLocked(sessionID string, firstRole byte, firstRemote string, secondRole byte, secondRemote string) {
	now := time.Now().UTC()
	stats := b.sessionStats[sessionID]
	if stats == nil {
		stats = &sessionStats{sessionID: sessionDigest(sessionID), startedAt: now}
		b.sessionStats[sessionID] = stats
	}
	stats.state = "active"
	stats.updatedAt = now
	if firstRole == roleSource {
		stats.sourceRemote = firstRemote
		stats.targetRemote = secondRemote
	} else {
		stats.targetRemote = firstRemote
		stats.sourceRemote = secondRemote
	}
}

func (b *Broker) addSessionBytes(sessionID string, sourceToTarget bool, count uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stats := b.sessionStats[sessionID]
	if stats == nil {
		return
	}
	stats.updatedAt = time.Now().UTC()
	if sourceToTarget {
		stats.sourceToTarget += count
		b.totalSourceBytes += count
	} else {
		stats.targetToSource += count
		b.totalTargetBytes += count
	}
}

func (b *Broker) markClosed(sessionID, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.markClosedLocked(sessionID, reason)
}

func (b *Broker) markClosedLocked(sessionID, reason string) {
	stats := b.sessionStats[sessionID]
	if stats == nil {
		return
	}
	now := time.Now().UTC()
	stats.state = "closed"
	stats.updatedAt = now
	stats.completedAt = now
	stats.closeReason = reason
	b.recentSessions = append([]SessionSnapshot{snapshotFromStats(stats)}, b.recentSessions...)
	if len(b.recentSessions) > 50 {
		b.recentSessions = b.recentSessions[:50]
	}
	delete(b.sessionStats, sessionID)
}

func snapshotFromStats(stats *sessionStats) SessionSnapshot {
	return SessionSnapshot{
		SessionID:      stats.sessionID,
		State:          stats.state,
		SourceRemote:   stats.sourceRemote,
		TargetRemote:   stats.targetRemote,
		SourceToTarget: stats.sourceToTarget,
		TargetToSource: stats.targetToSource,
		StartedAt:      stats.startedAt,
		UpdatedAt:      stats.updatedAt,
		CompletedAt:    stats.completedAt,
		CloseReason:    stats.closeReason,
	}
}

func sessionDigest(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:6])
}

func relayRoleLabel(role byte) string {
	switch role {
	case roleSource:
		return "source"
	case roleTarget:
		return "target"
	default:
		return "unknown"
	}
}
