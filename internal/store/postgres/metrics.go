package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

// MetricsStore handles all metrics-related database operations.
type MetricsStore struct {
	pool pooler
}

// NewMetricsStore creates a new MetricsStore.
func NewMetricsStore(pool pooler) *MetricsStore {
	return &MetricsStore{pool: pool}
}

// ============================================================
// Batch Inserts (COPY protocol for high throughput)
// ============================================================

// BatchInsertDeviceMetrics uses pgx COPY protocol for bulk insert.
func (s *MetricsStore) BatchInsertDeviceMetrics(ctx context.Context, rows []model.DeviceMetrics) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	columns := []string{
		"time", "device_id", "tenant_id",
		"cpu_usage", "memory_used", "memory_total",
		"load_avg_1", "load_avg_5", "load_avg_15",
		"uptime", "client_count", "temperature",
	}

	copyRows := make([][]interface{}, 0, len(rows))
	for _, r := range rows {
		copyRows = append(copyRows, []interface{}{
			r.Time, r.DeviceID, r.TenantID,
			r.CPUUsage, r.MemoryUsed, r.MemoryTotal,
			r.LoadAvg1, r.LoadAvg5, r.LoadAvg15,
			r.Uptime, r.ClientCount, r.Temperature,
		})
	}

	copied, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"device_metrics"},
		columns,
		pgx.CopyFromRows(copyRows),
	)
	if err != nil {
		return 0, fmt.Errorf("copy device_metrics: %w", err)
	}
	return copied, nil
}

// BatchInsertRadioMetrics uses pgx COPY protocol for bulk insert.
func (s *MetricsStore) BatchInsertRadioMetrics(ctx context.Context, rows []model.RadioMetrics) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	columns := []string{
		"time", "device_id", "tenant_id", "band",
		"channel", "channel_width", "tx_power", "noise_floor",
		"utilization", "client_count",
		"tx_bytes", "rx_bytes", "tx_packets", "rx_packets",
		"tx_errors", "rx_errors", "tx_retries",
	}

	copyRows := make([][]interface{}, 0, len(rows))
	for _, r := range rows {
		copyRows = append(copyRows, []interface{}{
			r.Time, r.DeviceID, r.TenantID, r.Band,
			r.Channel, r.ChannelWidth, r.TxPower, r.NoiseFloor,
			r.Utilization, r.ClientCount,
			r.TxBytes, r.RxBytes, r.TxPackets, r.RxPackets,
			r.TxErrors, r.RxErrors, r.TxRetries,
		})
	}

	copied, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"radio_metrics"},
		columns,
		pgx.CopyFromRows(copyRows),
	)
	if err != nil {
		return 0, fmt.Errorf("copy radio_metrics: %w", err)
	}
	return copied, nil
}

// ============================================================
// Client Sessions
// ============================================================

// OpenClientSession creates a new client session record.
func (s *MetricsStore) OpenClientSession(ctx context.Context, session *model.ClientSession) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO client_sessions (
			id, tenant_id, device_id, site_id,
			client_mac, client_ip, hostname,
			ssid, band, connected_at,
			avg_rssi, min_rssi, max_rssi
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, $11, $11
		)`,
		session.ID, session.TenantID, session.DeviceID, session.SiteID,
		session.ClientMAC, session.ClientIP, session.Hostname,
		session.SSID, session.Band, session.ConnectedAt,
		session.AvgRSSI,
	)
	if err != nil {
		return fmt.Errorf("open client session: %w", err)
	}
	return nil
}

// CloseClientSession marks a session as disconnected and records final stats.
func (s *MetricsStore) CloseClientSession(ctx context.Context, deviceID uuid.UUID, clientMAC string,
	disconnectedAt time.Time, reason string, txBytes, rxBytes int64) error {

	_, err := s.pool.Exec(ctx, `
		UPDATE client_sessions SET
			disconnected_at = $1,
			disconnect_reason = $2,
			duration_secs = EXTRACT(EPOCH FROM ($1 - connected_at))::integer,
			total_tx_bytes = $3,
			total_rx_bytes = $4
		WHERE device_id = $5
			AND client_mac = $6
			AND disconnected_at IS NULL
		`,
		disconnectedAt, reason, txBytes, rxBytes, deviceID, clientMAC,
	)
	if err != nil {
		return fmt.Errorf("close client session: %w", err)
	}
	return nil
}

// UpdateActiveSession updates stats for an active session (periodic update).
func (s *MetricsStore) UpdateActiveSession(ctx context.Context, deviceID uuid.UUID, clientMAC string,
	rssi int16, txRate, rxRate int, txBytes, rxBytes int64) error {

	_, err := s.pool.Exec(ctx, `
		UPDATE client_sessions SET
			total_tx_bytes = $1,
			total_rx_bytes = $2,
			avg_rssi = ($3 + COALESCE(avg_rssi, $3)) / 2,
			min_rssi = LEAST($3, COALESCE(min_rssi, $3)),
			max_rssi = GREATEST($3, COALESCE(max_rssi, $3)),
			avg_tx_rate = ($4 + COALESCE(avg_tx_rate, $4)) / 2,
			avg_rx_rate = ($5 + COALESCE(avg_rx_rate, $5)) / 2
		WHERE device_id = $6
			AND client_mac = $7
			AND disconnected_at IS NULL
		`,
		txBytes, rxBytes, rssi, txRate, rxRate, deviceID, clientMAC,
	)
	if err != nil {
		return fmt.Errorf("update active session: %w", err)
	}
	return nil
}

// CloseAllDeviceSessions closes all active sessions for a device (used on device disconnect).
func (s *MetricsStore) CloseAllDeviceSessions(ctx context.Context, deviceID uuid.UUID, reason string) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		UPDATE client_sessions SET
			disconnected_at = $1,
			disconnect_reason = $2,
			duration_secs = EXTRACT(EPOCH FROM ($1 - connected_at))::integer
		WHERE device_id = $3
			AND disconnected_at IS NULL
		`,
		now, reason, deviceID,
	)
	if err != nil {
		return fmt.Errorf("close all device sessions: %w", err)
	}
	return nil
}

// GetActiveSessions returns all active (not disconnected) sessions for a device.
func (s *MetricsStore) GetActiveSessions(ctx context.Context, deviceID uuid.UUID) ([]*model.ClientSession, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, device_id, site_id,
			client_mac::text, client_ip::text, hostname,
			ssid, band, connected_at, disconnected_at,
			duration_secs, total_tx_bytes, total_rx_bytes,
			avg_rssi, min_rssi, max_rssi,
			avg_tx_rate, avg_rx_rate,
			disconnect_reason, is_11r
		FROM client_sessions
		WHERE device_id = $1 AND disconnected_at IS NULL
		ORDER BY connected_at DESC`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("get active sessions: %w", err)
	}
	defer rows.Close()
	return scanClientSessions(rows)
}

// ListClientSessions queries client session history with filters.
func (s *MetricsStore) ListClientSessions(ctx context.Context, params model.ClientSessionQuery) ([]*model.ClientSession, int, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argIdx))
	args = append(args, params.TenantID)
	argIdx++

	if params.DeviceID != nil {
		conditions = append(conditions, fmt.Sprintf("device_id = $%d", argIdx))
		args = append(args, *params.DeviceID)
		argIdx++
	}
	if params.SiteID != nil {
		conditions = append(conditions, fmt.Sprintf("site_id = $%d", argIdx))
		args = append(args, *params.SiteID)
		argIdx++
	}
	if params.ClientMAC != "" {
		conditions = append(conditions, fmt.Sprintf("client_mac = $%d", argIdx))
		args = append(args, params.ClientMAC)
		argIdx++
	}
	if params.SSID != "" {
		conditions = append(conditions, fmt.Sprintf("ssid = $%d", argIdx))
		args = append(args, params.SSID)
		argIdx++
	}
	if params.Band != "" {
		conditions = append(conditions, fmt.Sprintf("band = $%d", argIdx))
		args = append(args, params.Band)
		argIdx++
	}
	if params.Active != nil {
		if *params.Active {
			conditions = append(conditions, "disconnected_at IS NULL")
		} else {
			conditions = append(conditions, "disconnected_at IS NOT NULL")
		}
	}
	if params.Start != nil {
		conditions = append(conditions, fmt.Sprintf("connected_at >= $%d", argIdx))
		args = append(args, *params.Start)
		argIdx++
	}
	if params.End != nil {
		conditions = append(conditions, fmt.Sprintf("connected_at <= $%d", argIdx))
		args = append(args, *params.End)
		argIdx++
	}

	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	// Count
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM client_sessions %s", whereClause)
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count client sessions: %w", err)
	}

	if params.Limit <= 0 {
		params.Limit = 50
	}
	if params.Limit > 200 {
		params.Limit = 200
	}

	query := fmt.Sprintf(`
		SELECT id, tenant_id, device_id, site_id,
			client_mac::text, client_ip::text, hostname,
			ssid, band, connected_at, disconnected_at,
			duration_secs, total_tx_bytes, total_rx_bytes,
			avg_rssi, min_rssi, max_rssi,
			avg_tx_rate, avg_rx_rate,
			disconnect_reason, is_11r
		FROM client_sessions %s
		ORDER BY connected_at DESC
		LIMIT $%d OFFSET $%d`,
		whereClause, argIdx, argIdx+1)

	args = append(args, params.Limit, params.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query client sessions: %w", err)
	}
	defer rows.Close()

	sessions, err := scanClientSessions(rows)
	if err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}

// ============================================================
// Metrics Queries (auto-resolution: raw → hourly → daily)
// ============================================================

// QueryDeviceMetrics returns device metrics at the requested resolution.
func (s *MetricsStore) QueryDeviceMetrics(ctx context.Context, q model.MetricsQuery) ([]model.DeviceMetricsResponse, error) {
	resolution := q.AutoResolution()

	var query string
	switch resolution {
	case "raw":
		query = `
			SELECT time, cpu_usage::float8, cpu_usage::float8,
				CASE WHEN memory_total > 0 THEN memory_used::float8 / memory_total * 100 ELSE NULL END,
				client_count::int, client_count::float8
			FROM device_metrics
			WHERE device_id = $1 AND tenant_id = $2
				AND time >= $3 AND time <= $4
			ORDER BY time ASC`
	case "1h":
		query = `
			SELECT bucket AS time, avg_cpu, max_cpu, avg_mem_pct,
				max_clients::int, avg_clients
			FROM device_metrics_hourly
			WHERE device_id = $1 AND tenant_id = $2
				AND bucket >= $3 AND bucket <= $4
			ORDER BY bucket ASC`
	case "1d":
		query = `
			SELECT bucket AS time, avg_cpu, max_cpu, avg_mem_pct,
				max_clients::int, avg_clients
			FROM device_metrics_daily
			WHERE device_id = $1 AND tenant_id = $2
				AND bucket >= $3 AND bucket <= $4
			ORDER BY bucket ASC`
	default:
		return nil, fmt.Errorf("unsupported resolution: %s", resolution)
	}

	rows, err := s.pool.Query(ctx, query, q.DeviceID, q.TenantID, q.Start, q.End)
	if err != nil {
		return nil, fmt.Errorf("query device metrics (%s): %w", resolution, err)
	}
	defer rows.Close()

	var results []model.DeviceMetricsResponse
	for rows.Next() {
		var r model.DeviceMetricsResponse
		if err := rows.Scan(&r.Time, &r.CPUAvg, &r.CPUMax, &r.MemPctAvg, &r.ClientsMax, &r.ClientsAvg); err != nil {
			return nil, fmt.Errorf("scan device metrics row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// QueryRadioMetrics returns radio metrics at the requested resolution.
func (s *MetricsStore) QueryRadioMetrics(ctx context.Context, q model.MetricsQuery) ([]model.RadioMetricsResponse, error) {
	resolution := q.AutoResolution()

	var query string
	var args []interface{}
	argIdx := 1

	baseConditions := fmt.Sprintf("device_id = $%d AND tenant_id = $%d", argIdx, argIdx+1)
	args = append(args, q.DeviceID, q.TenantID)
	argIdx += 2

	if q.Band != "" {
		baseConditions += fmt.Sprintf(" AND band = $%d", argIdx)
		args = append(args, q.Band)
		argIdx++
	}

	timeCondition := fmt.Sprintf("$%d", argIdx)
	args = append(args, q.Start)
	argIdx++
	timeCondition2 := fmt.Sprintf("$%d", argIdx)
	args = append(args, q.End)

	switch resolution {
	case "raw":
		query = fmt.Sprintf(`
			SELECT time, band,
				utilization::float8, utilization::float8,
				client_count::int,
				tx_bytes, rx_bytes, tx_retries, noise_floor::float8
			FROM radio_metrics
			WHERE %s AND time >= %s AND time <= %s
			ORDER BY time ASC, band`, baseConditions, timeCondition, timeCondition2)
	case "1h":
		query = fmt.Sprintf(`
			SELECT bucket AS time, band,
				avg_utilization, max_utilization,
				max_clients::int,
				total_tx_bytes, total_rx_bytes, total_retries, avg_noise_floor
			FROM radio_metrics_hourly
			WHERE %s AND bucket >= %s AND bucket <= %s
			ORDER BY bucket ASC, band`, baseConditions, timeCondition, timeCondition2)
	case "1d":
		query = fmt.Sprintf(`
			SELECT bucket AS time, band,
				avg_utilization, max_utilization,
				max_clients::int,
				total_tx_bytes, total_rx_bytes, total_retries, avg_noise_floor
			FROM radio_metrics_daily
			WHERE %s AND bucket >= %s AND bucket <= %s
			ORDER BY bucket ASC, band`, baseConditions, timeCondition, timeCondition2)
	default:
		return nil, fmt.Errorf("unsupported resolution: %s", resolution)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query radio metrics (%s): %w", resolution, err)
	}
	defer rows.Close()

	var results []model.RadioMetricsResponse
	for rows.Next() {
		var r model.RadioMetricsResponse
		if err := rows.Scan(&r.Time, &r.Band, &r.UtilAvg, &r.UtilMax, &r.ClientsMax,
			&r.TotalTxBytes, &r.TotalRxBytes, &r.TotalRetries, &r.NoiseFloorAvg); err != nil {
			return nil, fmt.Errorf("scan radio metrics row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// QuerySiteMetrics returns aggregated metrics across all devices in a site.

func (s *MetricsStore) QuerySiteMetrics(ctx context.Context, tenantID, siteID uuid.UUID,
	start, end time.Time, resolution string) ([]model.SiteMetricsResponse, error) {

	var query string

	switch resolution {
	case "1d":
		query = `
			SELECT m.bucket AS time,
				COUNT(DISTINCT m.device_id)::int AS total_devices,
				0::int AS online_devices,
				COALESCE(SUM(m.max_clients), 0)::int AS total_clients,
				AVG(m.avg_cpu)::float8 AS avg_cpu,
				MAX(m.max_cpu)::float8 AS max_cpu,
				AVG(m.avg_mem_pct)::float8 AS avg_mem_pct
			FROM device_metrics_daily m
			JOIN devices d ON d.id = m.device_id
			WHERE d.site_id = $1 AND m.tenant_id = $2
				AND m.bucket >= $3 AND m.bucket <= $4
			GROUP BY m.bucket
			ORDER BY m.bucket ASC`
	case "1h":
		query = `
			SELECT m.bucket AS time,
				COUNT(DISTINCT m.device_id)::int AS total_devices,
				0::int AS online_devices,
				COALESCE(SUM(m.max_clients), 0)::int AS total_clients,
				AVG(m.avg_cpu)::float8 AS avg_cpu,
				MAX(m.max_cpu)::float8 AS max_cpu,
				AVG(m.avg_mem_pct)::float8 AS avg_mem_pct
			FROM device_metrics_hourly m
			JOIN devices d ON d.id = m.device_id
			WHERE d.site_id = $1 AND m.tenant_id = $2
				AND m.bucket >= $3 AND m.bucket <= $4
			GROUP BY m.bucket
			ORDER BY m.bucket ASC`
	default:
		query = `
			SELECT m.time,
				COUNT(DISTINCT m.device_id)::int AS total_devices,
				0::int AS online_devices,
				COALESCE(SUM(m.client_count), 0)::int AS total_clients,
				AVG(m.cpu_usage)::float8 AS avg_cpu,
				MAX(m.cpu_usage)::float8 AS max_cpu,
				AVG(CASE WHEN m.memory_total > 0 THEN m.memory_used::float / m.memory_total * 100 ELSE NULL END)::float8 AS avg_mem_pct
			FROM device_metrics m
			JOIN devices d ON d.id = m.device_id
			WHERE d.site_id = $1 AND m.tenant_id = $2
				AND m.time >= $3 AND m.time <= $4
			GROUP BY m.time
			ORDER BY m.time ASC`
	}

	rows, err := s.pool.Query(ctx, query, siteID, tenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("query site metrics: %w", err)
	}
	defer rows.Close()

	var results []model.SiteMetricsResponse
	for rows.Next() {
		var r model.SiteMetricsResponse
		if err := rows.Scan(&r.Time, &r.TotalDevices, &r.OnlineDevices, &r.TotalClients,
			&r.AvgCPU, &r.MaxCPU, &r.AvgMemPct); err != nil {
			return nil, fmt.Errorf("scan site metrics row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ============================================================
// Helpers
// ============================================================

func scanClientSessions(rows pgx.Rows) ([]*model.ClientSession, error) {
	var sessions []*model.ClientSession
	for rows.Next() {
		var s model.ClientSession
		if err := rows.Scan(
			&s.ID, &s.TenantID, &s.DeviceID, &s.SiteID,
			&s.ClientMAC, &s.ClientIP, &s.Hostname,
			&s.SSID, &s.Band, &s.ConnectedAt, &s.DisconnectedAt,
			&s.DurationSecs, &s.TotalTxBytes, &s.TotalRxBytes,
			&s.AvgRSSI, &s.MinRSSI, &s.MaxRSSI,
			&s.AvgTxRate, &s.AvgRxRate,
			&s.DisconnectReason, &s.Is11r,
		); err != nil {
			return nil, fmt.Errorf("scan client session: %w", err)
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}
