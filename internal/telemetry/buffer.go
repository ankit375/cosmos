package telemetry

import (
	"sync"

	"github.com/yourorg/cloudctrl/internal/model"
)

// ClientEvent represents a client connect/disconnect/roam detected by the diff engine.
type ClientEvent struct {
	model.ClientInfo
	DeviceID  [16]byte // uuid bytes
	TenantID  [16]byte
	SiteID    *[16]byte
	EventType string // "connect", "disconnect", "roam"
	OldBand   string // for roam
}

// MetricsBatch holds one batch of metrics waiting to be flushed.
type MetricsBatch struct {
	DeviceMetrics []model.DeviceMetrics
	RadioMetrics  []model.RadioMetrics
	ClientEvents  []ClientEvent
}

// Reset clears the batch for reuse without reallocating.
func (b *MetricsBatch) Reset() {
	b.DeviceMetrics = b.DeviceMetrics[:0]
	b.RadioMetrics = b.RadioMetrics[:0]
	b.ClientEvents = b.ClientEvents[:0]
}

// Size returns the total number of items in the batch.
func (b *MetricsBatch) Size() int {
	return len(b.DeviceMetrics) + len(b.RadioMetrics) + len(b.ClientEvents)
}

// DoubleBuffer implements the double-buffer pattern for lock-free writing.
// Writers append to the active buffer.
// The flusher swaps buffers and drains the inactive one.
type DoubleBuffer struct {
	mu      sync.Mutex
	buffers [2]*MetricsBatch
	active  int // 0 or 1
}

// NewDoubleBuffer creates a new double buffer with pre-allocated capacity.
func NewDoubleBuffer(capacity int) *DoubleBuffer {
	return &DoubleBuffer{
		buffers: [2]*MetricsBatch{
			{
				DeviceMetrics: make([]model.DeviceMetrics, 0, capacity),
				RadioMetrics:  make([]model.RadioMetrics, 0, capacity*2),
				ClientEvents:  make([]ClientEvent, 0, capacity),
			},
			{
				DeviceMetrics: make([]model.DeviceMetrics, 0, capacity),
				RadioMetrics:  make([]model.RadioMetrics, 0, capacity*2),
				ClientEvents:  make([]ClientEvent, 0, capacity),
			},
		},
		active: 0,
	}
}

// Append adds metrics to the active buffer. Lock is held only for the append.
func (db *DoubleBuffer) Append(dm *model.DeviceMetrics, radios []model.RadioMetrics, events []ClientEvent) {
	db.mu.Lock()
	buf := db.buffers[db.active]
	if dm != nil {
		buf.DeviceMetrics = append(buf.DeviceMetrics, *dm)
	}
	buf.RadioMetrics = append(buf.RadioMetrics, radios...)
	buf.ClientEvents = append(buf.ClientEvents, events...)
	db.mu.Unlock()

	// Update gauges
	bufferDeviceMetricsSize.Set(float64(len(buf.DeviceMetrics)))
	bufferRadioMetricsSize.Set(float64(len(buf.RadioMetrics)))
	bufferClientEventsSize.Set(float64(len(buf.ClientEvents)))
}

// Swap atomically swaps the active buffer and returns the old one for flushing.
// After swap, the returned batch is exclusively owned by the caller.
func (db *DoubleBuffer) Swap() *MetricsBatch {
	db.mu.Lock()
	old := db.buffers[db.active]
	db.active = 1 - db.active
	// Reset the new active buffer
	db.buffers[db.active].Reset()
	db.mu.Unlock()

	bufferSwapTotal.Inc()
	bufferDeviceMetricsSize.Set(0)
	bufferRadioMetricsSize.Set(0)
	bufferClientEventsSize.Set(0)

	return old
}

// ActiveSize returns the current size of the active buffer.
func (db *DoubleBuffer) ActiveSize() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.buffers[db.active].Size()
}
