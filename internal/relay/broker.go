package relay

import (
	"bytes"
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

type controlPeer struct {
	connection   net.Conn
	registration registration
	outgoing     chan controlMessage
	done         chan struct{}
	closeOnce    sync.Once
}

type controlSession struct {
	source  *controlPeer
	target  *controlPeer
	pending *controlPeer
}

type waitingDataSession struct {
	connection net.Conn
	join       sessionJoin
	ready      chan pairResult
	done       chan struct{}
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
	controls            map[string]*controlSession
	dataWaiting         map[string]*waitingDataSession
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
		controls:            make(map[string]*controlSession),
		dataWaiting:         make(map[string]*waitingDataSession),
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
	version := []byte{0}
	_, firstErr := io.ReadFull(connection, version)
	registrationContextErr := registrationContext.Err()
	stopDeadline()
	cancelRegistration()
	if firstErr != nil {
		b.logger.Warn("Relay registration failed", "remote", remote, "error", firstErr)
		if registrationContextErr != nil {
			return registrationContextErr
		}
		return firstErr
	}

	reader := io.MultiReader(bytes.NewReader(version), connection)
	switch version[0] {
	case legacyRegistrationVersion, registrationVersion:
		return b.handleLegacyPair(ctx, connection, reader, remote)
	case controlJoinVersion:
		return b.handleControl(ctx, connection, reader, remote)
	case sessionJoinVersion:
		return b.handleDataSession(ctx, connection, reader, remote)
	default:
		err := fmt.Errorf("unsupported Relay protocol version %d", version[0])
		b.logger.Warn("Relay registration failed", "remote", remote, "error", err)
		return err
	}
}

func (b *Broker) handleLegacyPair(ctx context.Context, connection net.Conn, reader io.Reader, remote string) error {
	registration, err := readRegistration(reader)
	if err != nil {
		b.logger.Warn("Relay registration failed", "remote", remote, "error", err)
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

func (b *Broker) handleControl(ctx context.Context, connection net.Conn, reader io.Reader, remote string) error {
	registration, err := readControlJoin(reader)
	if err != nil {
		b.logger.Warn("Relay control join failed", "remote", remote, "error", err)
		return err
	}
	sessionLabel := sessionDigest(registration.sessionID)
	role := relayRoleLabel(registration.role)
	if !b.authorize(registration) {
		b.logger.Warn("Relay access token rejected", "remote", remote, "session", sessionLabel, "role", role)
		return errors.New("Relay access token is invalid")
	}
	peer := &controlPeer{
		connection:   connection,
		registration: registration,
		outgoing:     make(chan controlMessage, 128),
		done:         make(chan struct{}),
	}
	remove := b.registerControl(peer)
	defer remove()
	b.logger.Info("Relay control joined", "remote", remote, "session", sessionLabel, "role", role, "token_present", registration.accessTokenPresent)
	if err := writeAll(connection, []byte{1}); err != nil {
		return fmt.Errorf("confirm Relay control join: %w", err)
	}

	incoming := make(chan controlMessage, 1)
	readErrors := make(chan error, 1)
	go func() {
		for {
			message, err := readControlMessage(connection)
			if err != nil {
				readErrors <- err
				return
			}
			incoming <- message
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErrors:
			b.logger.Info("Relay control closed", "remote", remote, "session", sessionLabel, "role", role, "error", err)
			return err
		case message := <-incoming:
			switch message.messageType {
			case controlMessageRequestSession:
				b.requestDataSession(peer)
			case controlMessagePing:
				peer.send(controlMessage{messageType: controlMessagePong})
			case controlMessageWake:
				b.forwardWake(peer)
			}
		case message := <-peer.outgoing:
			if err := writeControlMessage(connection, message.messageType, message.payload); err != nil {
				return fmt.Errorf("write Relay control message: %w", err)
			}
		}
	}
}

func (b *Broker) handleDataSession(ctx context.Context, connection net.Conn, reader io.Reader, remote string) error {
	join, err := readSessionJoin(reader)
	if err != nil {
		b.logger.Warn("Relay data session join failed", "remote", remote, "error", err)
		return err
	}
	sessionLabel := sessionDigest(join.sessionID)
	role := relayRoleLabel(join.role)
	outcome, err := b.pairDataSession(ctx, connection, join)
	if err != nil {
		b.logger.Warn("Relay data session pairing failed", "remote", remote, "session", sessionLabel, "role", role, "error", err)
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
		return fmt.Errorf("confirm Relay data session: %w", err)
	}
	if err := writeAll(outcome.peer, []byte{1}); err != nil {
		return fmt.Errorf("confirm Relay peer data session: %w", err)
	}
	return b.forward(ctx, connection, outcome.peer, join.sessionID, join.role)
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

func (p *controlPeer) send(message controlMessage) bool {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case p.outgoing <- message:
		return true
	case <-p.done:
		return false
	case <-timer.C:
		return false
	}
}

func (p *controlPeer) closeDone() {
	p.closeOnce.Do(func() {
		close(p.done)
	})
}

func (b *Broker) registerControl(peer *controlPeer) func() {
	var replaced *controlPeer
	var invitations []controlMessageTarget
	b.mu.Lock()
	state := b.controls[peer.registration.sessionID]
	if state == nil {
		state = &controlSession{}
		b.controls[peer.registration.sessionID] = state
	}
	if peer.registration.role == roleSource {
		replaced = state.source
		state.source = peer
	} else {
		replaced = state.target
		state.target = peer
	}
	if replaced != nil {
		replaced.closeDone()
	}
	invitations = b.pendingInvitationsLocked(peer.registration.sessionID, state)
	b.mu.Unlock()
	if replaced != nil {
		_ = replaced.connection.Close()
	}
	sendControlMessages(invitations)
	return func() {
		b.removeControl(peer)
	}
}

type controlMessageTarget struct {
	peer    *controlPeer
	message controlMessage
}

func sendControlMessages(messages []controlMessageTarget) {
	for _, target := range messages {
		target.peer.send(target.message)
	}
}

func (b *Broker) requestDataSession(peer *controlPeer) {
	var messages []controlMessageTarget
	b.mu.Lock()
	state := b.controls[peer.registration.sessionID]
	if state == nil {
		state = &controlSession{}
		b.controls[peer.registration.sessionID] = state
	}
	state.pending = peer
	messages = b.pendingInvitationsLocked(peer.registration.sessionID, state)
	b.mu.Unlock()
	sendControlMessages(messages)
}

func (b *Broker) forwardWake(peer *controlPeer) {
	var target *controlPeer
	b.mu.Lock()
	state := b.controls[peer.registration.sessionID]
	if state != nil {
		if peer.registration.role == roleSource {
			target = state.target
		} else {
			target = state.source
		}
	}
	b.mu.Unlock()
	if target != nil {
		target.send(controlMessage{messageType: controlMessageWake})
	}
}

func (b *Broker) pendingInvitationsLocked(sessionID string, state *controlSession) []controlMessageTarget {
	if state == nil || state.pending == nil || state.source == nil || state.target == nil {
		return nil
	}
	requester := state.pending
	if requester != state.source && requester != state.target {
		state.pending = nil
		return nil
	}
	if !sameToken(state.source.registration.tokenHash, state.target.registration.tokenHash) {
		requester.send(controlMessage{messageType: controlMessageError, payload: []byte("Relay session authentication failed")})
		state.pending = nil
		return nil
	}
	key, err := newSessionKey()
	if err != nil {
		requester.send(controlMessage{messageType: controlMessageError, payload: []byte(err.Error())})
		state.pending = nil
		return nil
	}
	state.pending = nil
	sessionKey := hex.EncodeToString(key[:])
	b.dataWaiting[sessionKey] = nil
	payload := key[:]
	b.logger.Info("Relay data session invited", "session", sessionDigest(sessionID), "requester", relayRoleLabel(requester.registration.role))
	return []controlMessageTarget{
		{peer: state.source, message: controlMessage{messageType: controlMessageInviteSession, payload: payload}},
		{peer: state.target, message: controlMessage{messageType: controlMessageInviteSession, payload: payload}},
	}
}

func (b *Broker) removeControl(peer *controlPeer) {
	peer.closeDone()
	b.mu.Lock()
	state := b.controls[peer.registration.sessionID]
	if state != nil {
		if state.source == peer {
			state.source = nil
		}
		if state.target == peer {
			state.target = nil
		}
		if state.pending == peer {
			state.pending = nil
		}
		if state.source == nil && state.target == nil {
			delete(b.controls, peer.registration.sessionID)
		}
	}
	b.mu.Unlock()
}

func (b *Broker) pairDataSession(ctx context.Context, connection net.Conn, join sessionJoin) (pairOutcome, error) {
	sessionKey := hex.EncodeToString(join.key[:])
	sessionLabel := sessionDigest(join.sessionID)
	role := relayRoleLabel(join.role)
	b.mu.Lock()
	if waiting, exists := b.dataWaiting[sessionKey]; exists && waiting != nil {
		if waiting.join.role == join.role {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay data session already has this role")
		}
		if waiting.join.sessionID != join.sessionID {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay data session ID mismatch")
		}
		if b.active >= b.maxActive {
			b.mu.Unlock()
			return pairOutcome{}, errors.New("Relay active session limit reached")
		}
		delete(b.dataWaiting, sessionKey)
		b.active++
		b.markActiveLocked(join.sessionID, waiting.join.role, waiting.connection.RemoteAddr().String(), join.role, connection.RemoteAddr().String())
		b.mu.Unlock()
		b.logger.Info("Relay data peer matched", "session", sessionLabel, "role", role)
		waiting.ready <- pairResult{peer: connection}
		close(waiting.ready)
		return pairOutcome{owner: false, done: waiting.done}, nil
	}
	if _, invited := b.dataWaiting[sessionKey]; !invited {
		b.mu.Unlock()
		return pairOutcome{}, errors.New("Relay data session was not invited")
	}
	waiting := &waitingDataSession{
		connection: connection,
		join:       join,
		ready:      make(chan pairResult, 1),
		done:       make(chan struct{}),
	}
	b.dataWaiting[sessionKey] = waiting
	b.markWaitingLocked(join.sessionID, join.role, connection.RemoteAddr().String())
	b.mu.Unlock()
	b.logger.Info("Relay data peer waiting", "session", sessionLabel, "role", role, "timeout", b.waitTimeout.String())

	timer := time.NewTimer(b.waitTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		b.removeWaitingData(sessionKey, waiting)
		return pairOutcome{}, ctx.Err()
	case <-timer.C:
		b.removeWaitingData(sessionKey, waiting)
		b.logger.Warn("Relay data pairing timed out", "session", sessionLabel, "role", role)
		return pairOutcome{}, errors.New("Relay data pairing timed out")
	case result := <-waiting.ready:
		return pairOutcome{peer: result.peer, owner: true, done: waiting.done}, result.err
	}
}

func (b *Broker) removeWaitingData(sessionKey string, waiting *waitingDataSession) {
	b.mu.Lock()
	if b.dataWaiting[sessionKey] == waiting {
		delete(b.dataWaiting, sessionKey)
		b.markClosedLocked(waiting.join.sessionID, "waiting data session removed")
	}
	b.mu.Unlock()
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
				b.addSessionBytes(sessionID, sourceToTarget, uint64(count))
				if err := writeAll(destination, buffer[:count]); err != nil {
					results <- copyResult{err: err}
					return
				}
				transferred += int64(count)
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

// ClearHistory clears accumulated Relay traffic totals and closed session history.
// Active and waiting sessions remain untouched.
func (b *Broker) ClearHistory() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recentSessions = nil
	b.totalSourceBytes = 0
	b.totalTargetBytes = 0
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
