package command

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// ============================================================
// DEVICE SENDER INTERFACE (avoid circular imports)
// ============================================================

// DeviceSender is the interface for sending messages to devices.
type DeviceSender interface {
	SendMessage(deviceID uuid.UUID, channel uint8, msgType uint16, flags uint8, payload interface{}) (uint32, error)
	IsConnected(deviceID uuid.UUID) bool
}

// ============================================================
// IN-FLIGHT COMMAND TRACKING
// ============================================================

// inflightCommand tracks a command that has been sent and is awaiting ACK.
type inflightCommand struct {
	CommandID     uuid.UUID
	DeviceID      uuid.UUID
	TenantID      uuid.UUID
	CommandType   string
	CorrelationID string
	SentAt        time.Time
	Timeout       time.Duration
	RetryCount    int
	MaxRetries    int
}

// ============================================================
// PER-DEVICE QUEUE
// ============================================================

// deviceQueue is a priority-ordered in-memory queue for a single device.
type deviceQueue struct {
	commands []*model.QueuedCommand
}

// push adds a command, maintaining priority order (lower number = higher priority).
func (q *deviceQueue) push(cmd *model.QueuedCommand) {
	// Insert in priority order
	inserted := false
	for i, existing := range q.commands {
		if cmd.Priority < existing.Priority {
			// Insert before this element
			q.commands = append(q.commands, nil)
			copy(q.commands[i+1:], q.commands[i:])
			q.commands[i] = cmd
			inserted = true
			break
		}
	}
	if !inserted {
		q.commands = append(q.commands, cmd)
	}
}

// pop removes and returns the highest-priority command, or nil.
func (q *deviceQueue) pop() *model.QueuedCommand {
	if len(q.commands) == 0 {
		return nil
	}
	cmd := q.commands[0]
	q.commands = q.commands[1:]
	return cmd
}

// peek returns the highest-priority command without removing it, or nil.
func (q *deviceQueue) peek() *model.QueuedCommand {
	if len(q.commands) == 0 {
		return nil
	}
	return q.commands[0]
}

// remove removes a command by ID. Returns true if found and removed.
func (q *deviceQueue) remove(cmdID uuid.UUID) bool {
	for i, cmd := range q.commands {
		if cmd.ID == cmdID {
			q.commands = append(q.commands[:i], q.commands[i+1:]...)
			return true
		}
	}
	return false
}

// len returns the number of queued commands.
func (q *deviceQueue) len() int {
	return len(q.commands)
}

// ============================================================
// COMMAND MANAGER
// ============================================================

// ManagerConfig holds configuration for the command manager.
type ManagerConfig struct {
	CommandTimeout    time.Duration // How long to wait for ACK before retry
	TimeoutCheckInterval time.Duration // How often to check for timed-out commands
	DefaultMaxRetries int           // Default max retries per command
	DefaultPriority   int           // Default priority (lower = higher priority)
	DefaultTTL        time.Duration // Default command expiry TTL
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		CommandTimeout:       30 * time.Second,
		TimeoutCheckInterval: 5 * time.Second,
		DefaultMaxRetries:    3,
		DefaultPriority:      5,
		DefaultTTL:           10 * time.Minute,
	}
}

// Manager orchestrates command queuing, dispatch, ACK tracking, and retries.
type Manager struct {
	pgStore *pgstore.Store
	sender  DeviceSender
	cfg     ManagerConfig
	logger  *zap.Logger

	// In-memory queues: deviceID → queue
	mu     sync.Mutex
	queues map[uuid.UUID]*deviceQueue

	// In-flight commands: correlationID → inflight
	inflightMu sync.RWMutex
	inflight   map[string]*inflightCommand

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager creates a new command manager.
func NewManager(
	pgStore *pgstore.Store,
	sender DeviceSender,
	cfg ManagerConfig,
	logger *zap.Logger,
) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		pgStore:  pgStore,
		sender:   sender,
		cfg:      cfg,
		logger:   logger.Named("commandmgr"),
		queues:   make(map[uuid.UUID]*deviceQueue),
		inflight: make(map[string]*inflightCommand),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// ============================================================
// LIFECYCLE
// ============================================================

// Start starts the command manager's background workers.
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.runTimeoutChecker()

	m.logger.Info("command manager started",
		zap.Duration("command_timeout", m.cfg.CommandTimeout),
		zap.Duration("timeout_check_interval", m.cfg.TimeoutCheckInterval),
		zap.Int("default_max_retries", m.cfg.DefaultMaxRetries),
	)
}

// Stop gracefully stops the command manager.
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
	m.logger.Info("command manager stopped")
}

// ============================================================
// COMMAND QUEUING
// ============================================================

// EnqueueCommand creates a new command and either sends it immediately or queues it.
func (m *Manager) EnqueueCommand(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	commandType string,
	payload json.RawMessage,
	priority int,
	createdBy *uuid.UUID,
) (*model.QueuedCommand, error) {

	if priority <= 0 {
		priority = m.cfg.DefaultPriority
	}

	// Ensure payload is never nil (DB column is NOT NULL DEFAULT '{}')
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}

	expiresAt := time.Now().Add(m.cfg.DefaultTTL)

	cmd := &model.QueuedCommand{
		ID:          uuid.New(),
		TenantID:    tenantID,
		DeviceID:    deviceID,
		CommandType: commandType,
		Payload:     payload,
		Status:      model.CommandStatusQueued,
		Priority:    priority,
		MaxRetries:  m.cfg.DefaultMaxRetries,
		RetryCount:  0,
		ExpiresAt:   &expiresAt,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now(),
	}

	// Persist to DB
	if err := m.pgStore.Commands.Create(ctx, cmd); err != nil {
		return nil, fmt.Errorf("persist command: %w", err)
	}

	commandsQueuedTotal.WithLabelValues(commandType).Inc()

	m.logger.Info("command enqueued",
		zap.String("command_id", cmd.ID.String()),
		zap.String("device_id", deviceID.String()),
		zap.String("command_type", commandType),
		zap.Int("priority", priority),
	)

	// Try to send immediately if device is online
	if m.sender.IsConnected(deviceID) {
		m.dispatchCommand(cmd)
	} else {
		// Add to in-memory queue for delivery on reconnect
		m.enqueueInMemory(cmd)
		m.logger.Debug("device offline, command queued for later delivery",
			zap.String("device_id", deviceID.String()),
			zap.String("command_id", cmd.ID.String()),
		)
	}

	return cmd, nil
}

// enqueueInMemory adds a command to the in-memory per-device queue.
func (m *Manager) enqueueInMemory(cmd *model.QueuedCommand) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues[cmd.DeviceID]
	if !ok {
		q = &deviceQueue{}
		m.queues[cmd.DeviceID] = q
	}
	q.push(cmd)

	commandsQueueDepth.WithLabelValues(cmd.DeviceID.String()).Set(float64(q.len()))
}

// ============================================================
// COMMAND DISPATCH
// ============================================================

// dispatchCommand sends a command to a connected device.
func (m *Manager) dispatchCommand(cmd *model.QueuedCommand) {
	start := time.Now()

	correlationID := fmt.Sprintf("cmd-%s", cmd.ID.String())

	// Determine message type and build payload
	msgType, cmdPayload := m.buildCommandPayload(cmd)

	// Send via WebSocket
	_, err := m.sender.SendMessage(
		cmd.DeviceID,
		protocol.ChannelControl,
		msgType,
		protocol.FlagACKRequired,
		cmdPayload,
	)
	if err != nil {
		m.logger.Error("failed to send command",
			zap.String("command_id", cmd.ID.String()),
			zap.String("device_id", cmd.DeviceID.String()),
			zap.Error(err),
		)
		// Leave as queued — timeout checker will retry
		m.enqueueInMemory(cmd)
		return
	}

	// Update status to sent
	now := time.Now()
	cmd.Status = model.CommandStatusSent
	cmd.SentAt = &now
	cmd.CorrelationID = &correlationID

	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	if err := m.pgStore.Commands.UpdateStatus(ctx, cmd.ID, model.CommandStatusSent, correlationID); err != nil {
		m.logger.Error("failed to update command status to sent",
			zap.String("command_id", cmd.ID.String()),
			zap.Error(err),
		)
	}

	// Track in-flight
	m.inflightMu.Lock()
	m.inflight[correlationID] = &inflightCommand{
		CommandID:     cmd.ID,
		DeviceID:      cmd.DeviceID,
		TenantID:      cmd.TenantID,
		CommandType:   cmd.CommandType,
		CorrelationID: correlationID,
		SentAt:        now,
		Timeout:       m.cfg.CommandTimeout,
		RetryCount:    cmd.RetryCount,
		MaxRetries:    cmd.MaxRetries,
	}
	m.inflightMu.Unlock()

	commandsInflight.Set(float64(m.inflightCount()))
	commandsSentTotal.WithLabelValues(cmd.CommandType).Inc()
	commandDispatchDuration.Observe(time.Since(start).Seconds())

	m.logger.Info("command sent to device",
		zap.String("command_id", cmd.ID.String()),
		zap.String("device_id", cmd.DeviceID.String()),
		zap.String("command_type", cmd.CommandType),
		zap.String("correlation_id", correlationID),
	)
}

// buildCommandPayload creates the wire payload for a command.
func (m *Manager) buildCommandPayload(cmd *model.QueuedCommand) (uint16, interface{}) {
	timeout := int(m.cfg.CommandTimeout.Seconds())

	switch cmd.CommandType {
	case "reboot":
		return protocol.MsgReboot, &protocol.CommandPayload{
			Command: "reboot",
			Params:  cmd.Payload,
			Timeout: timeout,
		}
	case "locate":
		return protocol.MsgLEDLocate, &protocol.CommandPayload{
			Command: "locate",
			Params:  cmd.Payload,
			Timeout: timeout,
		}
	case "kick_client":
		return protocol.MsgKickClient, &protocol.CommandPayload{
			Command: "kick_client",
			Params:  cmd.Payload,
			Timeout: timeout,
		}
	case "wifi_scan":
		return protocol.MsgScanRequest, &protocol.CommandPayload{
			Command: "wifi_scan",
			Params:  cmd.Payload,
			Timeout: timeout,
		}
	default:
		// Generic command
		return protocol.MsgCommand, &protocol.CommandPayload{
			Command: cmd.CommandType,
			Params:  cmd.Payload,
			Timeout: timeout,
		}
	}
}

// ============================================================
// ACK HANDLING
// ============================================================

// HandleCommandResponse processes a command ACK/response from a device.
func (m *Manager) HandleCommandResponse(
	deviceID uuid.UUID,
	msgID uint32,
	success bool,
	result json.RawMessage,
	errMsg string,
) {
	// Find in-flight command by device (match the oldest sent command for this device)
	m.inflightMu.Lock()
	var matched *inflightCommand
	var matchedKey string
	for key, inf := range m.inflight {
		if inf.DeviceID == deviceID {
			if matched == nil || inf.SentAt.Before(matched.SentAt) {
				matched = inf
				matchedKey = key
			}
		}
	}
	if matched != nil {
		delete(m.inflight, matchedKey)
	}
	m.inflightMu.Unlock()

	if matched == nil {
		m.logger.Debug("command response received but no matching in-flight command",
			zap.String("device_id", deviceID.String()),
			zap.Uint32("msg_id", msgID),
		)
		return
	}

	commandsInflight.Set(float64(m.inflightCount()))

	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	if success {
		if err := m.pgStore.Commands.Complete(ctx, matched.CommandID, result); err != nil {
			m.logger.Error("failed to mark command completed",
				zap.String("command_id", matched.CommandID.String()),
				zap.Error(err),
			)
		}
		commandsCompletedTotal.WithLabelValues(matched.CommandType, "success").Inc()

		m.logger.Info("command completed successfully",
			zap.String("command_id", matched.CommandID.String()),
			zap.String("device_id", deviceID.String()),
			zap.String("command_type", matched.CommandType),
		)
	} else {
		if err := m.pgStore.Commands.Fail(ctx, matched.CommandID, errMsg); err != nil {
			m.logger.Error("failed to mark command failed",
				zap.String("command_id", matched.CommandID.String()),
				zap.Error(err),
			)
		}
		commandsCompletedTotal.WithLabelValues(matched.CommandType, "failed").Inc()

		m.logger.Warn("command failed on device",
			zap.String("command_id", matched.CommandID.String()),
			zap.String("device_id", deviceID.String()),
			zap.String("command_type", matched.CommandType),
			zap.String("error", errMsg),
		)
	}
}

// ============================================================
// RECONNECT: DELIVER QUEUED COMMANDS
// ============================================================

// DeliverQueuedCommands sends all queued commands to a device that just reconnected.
func (m *Manager) DeliverQueuedCommands(deviceID uuid.UUID) {
	// First check in-memory queue
	m.mu.Lock()
	q, ok := m.queues[deviceID]
	var cmds []*model.QueuedCommand
	if ok {
		for q.len() > 0 {
			cmds = append(cmds, q.pop())
		}
		delete(m.queues, deviceID)
	}
	m.mu.Unlock()

	// Also check DB for any queued commands not in memory
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	dbCmds, err := m.pgStore.Commands.GetPendingByDevice(ctx, deviceID)
	if err != nil {
		m.logger.Error("failed to load queued commands from DB on reconnect",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
	}

	// Merge: DB commands that aren't already in the in-memory list
	seen := make(map[uuid.UUID]struct{})
	for _, c := range cmds {
		seen[c.ID] = struct{}{}
	}
	for _, c := range dbCmds {
		if _, exists := seen[c.ID]; !exists {
			cmds = append(cmds, c)
		}
	}

	if len(cmds) == 0 {
		return
	}

	// Sort by priority (already sorted from queue, but DB commands need sorting)
	sortByPriority(cmds)

	m.logger.Info("delivering queued commands on reconnect",
		zap.String("device_id", deviceID.String()),
		zap.Int("count", len(cmds)),
	)

	for _, cmd := range cmds {
		// Check if expired
		if cmd.ExpiresAt != nil && time.Now().After(*cmd.ExpiresAt) {
			m.expireCommand(cmd)
			continue
		}
		m.dispatchCommand(cmd)
	}

	commandsQueueDepth.WithLabelValues(deviceID.String()).Set(0)
}

// sortByPriority sorts commands by priority (ascending), then by created_at.
func sortByPriority(cmds []*model.QueuedCommand) {
	for i := 1; i < len(cmds); i++ {
		for j := i; j > 0; j-- {
			if cmds[j].Priority < cmds[j-1].Priority ||
				(cmds[j].Priority == cmds[j-1].Priority && cmds[j].CreatedAt.Before(cmds[j-1].CreatedAt)) {
				cmds[j], cmds[j-1] = cmds[j-1], cmds[j]
			} else {
				break
			}
		}
	}
}

// ============================================================
// STARTUP RECOVERY
// ============================================================

// RecoverOnStartup loads pending/sent commands from DB and re-populates in-memory queues.
func (m *Manager) RecoverOnStartup(ctx context.Context) error {
	m.logger.Info("recovering command queues from database...")

	cmds, err := m.pgStore.Commands.GetAllPending(ctx)
	if err != nil {
		return fmt.Errorf("load pending commands: %w", err)
	}

	queued := 0
	expired := 0

	for _, cmd := range cmds {
		// Expire old commands
		if cmd.ExpiresAt != nil && time.Now().After(*cmd.ExpiresAt) {
			m.expireCommand(cmd)
			expired++
			continue
		}

		// Reset sent commands back to queued (controller restarted, ACK lost)
		if cmd.Status == model.CommandStatusSent {
			cmd.Status = model.CommandStatusQueued
			cmd.RetryCount++
			updateCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_ = m.pgStore.Commands.ResetToQueued(updateCtx, cmd.ID, cmd.RetryCount)
			cancel()
		}

		m.enqueueInMemory(cmd)
		queued++
	}

	m.logger.Info("command queue recovery complete",
		zap.Int("queued", queued),
		zap.Int("expired", expired),
	)

	return nil
}

// ============================================================
// BACKGROUND WORKERS
// ============================================================

// runTimeoutChecker periodically checks for timed-out in-flight commands.
func (m *Manager) runTimeoutChecker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.cfg.TimeoutCheckInterval)
	defer ticker.Stop()

	m.logger.Info("command timeout checker started",
		zap.Duration("interval", m.cfg.TimeoutCheckInterval),
		zap.Duration("command_timeout", m.cfg.CommandTimeout),
	)

	for {
		select {
		case <-ticker.C:
			m.checkTimeouts()
		case <-m.ctx.Done():
			m.logger.Info("command timeout checker stopped")
			return
		}
	}
}

// checkTimeouts finds timed-out in-flight commands and retries or fails them.
func (m *Manager) checkTimeouts() {
	start := time.Now()
	now := time.Now()

	m.inflightMu.Lock()
	var timedOut []*inflightCommand
	for key, inf := range m.inflight {
		if now.Sub(inf.SentAt) > inf.Timeout {
			timedOut = append(timedOut, inf)
			delete(m.inflight, key)
		}
	}
	m.inflightMu.Unlock()

	if len(timedOut) == 0 {
		commandTimeoutCheckDuration.Observe(time.Since(start).Seconds())
		return
	}

	commandsInflight.Set(float64(m.inflightCount()))

	for _, inf := range timedOut {
		m.logger.Warn("command timed out",
			zap.String("command_id", inf.CommandID.String()),
			zap.String("device_id", inf.DeviceID.String()),
			zap.String("command_type", inf.CommandType),
			zap.Int("retry_count", inf.RetryCount),
			zap.Int("max_retries", inf.MaxRetries),
		)

		if inf.RetryCount >= inf.MaxRetries {
			// Max retries exceeded — mark as failed
			m.failCommand(inf.CommandID, inf.CommandType, "max retries exceeded after timeout")
			continue
		}

		// Retry
		m.retryCommand(inf)
	}

	commandTimeoutCheckDuration.Observe(time.Since(start).Seconds())
}

// retryCommand re-queues a timed-out command for retry.
func (m *Manager) retryCommand(inf *inflightCommand) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	newRetryCount := inf.RetryCount + 1

	if err := m.pgStore.Commands.ResetToQueued(ctx, inf.CommandID, newRetryCount); err != nil {
		m.logger.Error("failed to reset command for retry",
			zap.String("command_id", inf.CommandID.String()),
			zap.Error(err),
		)
		return
	}

	commandsRetriedTotal.WithLabelValues(inf.CommandType).Inc()

	m.logger.Info("command queued for retry",
		zap.String("command_id", inf.CommandID.String()),
		zap.String("device_id", inf.DeviceID.String()),
		zap.Int("retry_count", newRetryCount),
	)

	// Load from DB and re-dispatch if device is connected
	cmd, err := m.pgStore.Commands.GetByID(ctx, inf.CommandID)
	if err != nil || cmd == nil {
		m.logger.Error("failed to reload command for retry",
			zap.String("command_id", inf.CommandID.String()),
			zap.Error(err),
		)
		return
	}

	if m.sender.IsConnected(inf.DeviceID) {
		m.dispatchCommand(cmd)
	} else {
		m.enqueueInMemory(cmd)
	}
}

// failCommand marks a command as failed permanently.
func (m *Manager) failCommand(cmdID uuid.UUID, cmdType, errMsg string) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	if err := m.pgStore.Commands.Fail(ctx, cmdID, errMsg); err != nil {
		m.logger.Error("failed to mark command as failed",
			zap.String("command_id", cmdID.String()),
			zap.Error(err),
		)
	}

	commandsExpiredTotal.WithLabelValues(cmdType).Inc()

	m.logger.Warn("command failed permanently",
		zap.String("command_id", cmdID.String()),
		zap.String("error", errMsg),
	)
}

// expireCommand marks a command as expired.
func (m *Manager) expireCommand(cmd *model.QueuedCommand) {
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	if err := m.pgStore.Commands.Expire(ctx, cmd.ID); err != nil {
		m.logger.Error("failed to mark command as expired",
			zap.String("command_id", cmd.ID.String()),
			zap.Error(err),
		)
	}

	commandsExpiredTotal.WithLabelValues(cmd.CommandType).Inc()

	m.logger.Debug("command expired",
		zap.String("command_id", cmd.ID.String()),
		zap.String("device_id", cmd.DeviceID.String()),
	)
}

// ============================================================
// QUERY HELPERS
// ============================================================

// GetDeviceCommands returns commands for a device (for API).
func (m *Manager) GetDeviceCommands(ctx context.Context, tenantID, deviceID uuid.UUID, limit, offset int) ([]*model.QueuedCommand, int, error) {
	return m.pgStore.Commands.ListByDevice(ctx, tenantID, deviceID, limit, offset)
}

// GetPendingCount returns the number of pending commands for a device.
func (m *Manager) GetPendingCount(deviceID uuid.UUID) int {
	m.mu.Lock()
	q, ok := m.queues[deviceID]
	count := 0
	if ok {
		count = q.len()
	}
	m.mu.Unlock()
	return count
}

// inflightCount returns the total in-flight commands.
func (m *Manager) inflightCount() int {
	m.inflightMu.RLock()
	defer m.inflightMu.RUnlock()
	return len(m.inflight)
}
