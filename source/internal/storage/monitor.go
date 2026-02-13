// Package storage implements the volume and database size monitoring loop.
// It periodically checks filesystem usage, inode consumption, and database
// file size, updating Prometheus metrics and logging warnings when
// configurable thresholds are exceeded.
package storage

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
)

// Monitor periodically inspects the storage volume and database to report
// usage metrics and detect storage pressure.
type Monitor struct {
	db      database.Database
	cfg     *config.Config
	metrics *metrics.Metrics
	logger  *zap.Logger
}

// NewMonitor creates a new Monitor with the provided dependencies.
func NewMonitor(db database.Database, cfg *config.Config, m *metrics.Metrics, logger *zap.Logger) *Monitor {
	return &Monitor{
		db:      db,
		cfg:     cfg,
		metrics: m,
		logger:  logger,
	}
}

// Start begins the storage monitoring loop, running at the configured
// monitor interval. The loop stops when ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.Storage.MonitorInterval.Duration)
	defer ticker.Stop()

	m.logger.Info("storage monitor started",
		zap.Duration("interval", m.cfg.Storage.MonitorInterval.Duration),
		zap.String("volume_path", m.cfg.Storage.VolumePath),
		zap.Int("warning_threshold", m.cfg.Storage.WarningThreshold),
		zap.Int("critical_threshold", m.cfg.Storage.CriticalThreshold),
	)

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("storage monitor stopping", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			if err := m.Check(ctx); err != nil {
				m.logger.Error("storage check failed", zap.Error(err))
			}
		}
	}
}

// Check performs a single storage check. It gathers filesystem statistics
// using syscall.Statfs, queries the database size, calculates usage
// percentages, updates all storage-related Prometheus metrics, and logs
// warnings when warning or critical thresholds are exceeded.
func (m *Monitor) Check(ctx context.Context) error {
	// Check for context cancellation before proceeding.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Gather filesystem statistics.
	var stat syscall.Statfs_t
	if err := syscall.Statfs(m.cfg.Storage.VolumePath, &stat); err != nil {
		return fmt.Errorf("statfs on %s: %w", m.cfg.Storage.VolumePath, err)
	}

	// Calculate volume metrics.
	blockSize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * blockSize
	availableBytes := stat.Bavail * blockSize
	usedBytes := totalBytes - (stat.Bfree * blockSize)

	// Calculate usage percentage (avoid division by zero).
	var usagePercent float64
	if totalBytes > 0 {
		usagePercent = (float64(usedBytes) / float64(totalBytes)) * 100.0
	}

	// Inode statistics.
	totalInodes := stat.Files
	usedInodes := stat.Files - stat.Ffree

	// Update volume metrics.
	m.metrics.StorageVolumeSizeBytes.Set(float64(totalBytes))
	m.metrics.StorageVolumeUsedBytes.Set(float64(usedBytes))
	m.metrics.StorageVolumeAvailableBytes.Set(float64(availableBytes))
	m.metrics.StorageVolumeUsagePercent.Set(usagePercent)
	m.metrics.StorageVolumeInodesTotal.Set(float64(totalInodes))
	m.metrics.StorageVolumeInodesUsed.Set(float64(usedInodes))

	// Get database file size.
	dbSizeBytes, err := m.db.GetDatabaseSizeBytes()
	if err != nil {
		m.logger.Error("failed to get database size", zap.Error(err))
		// Continue with the rest of the checks; this is not fatal.
	} else {
		m.metrics.DBSizeBytes.Set(float64(dbSizeBytes))
	}

	// Evaluate storage pressure thresholds.
	m.evaluatePressure(usagePercent)

	m.logger.Debug("storage check completed",
		zap.Float64("usage_percent", usagePercent),
		zap.Uint64("total_bytes", totalBytes),
		zap.Uint64("used_bytes", usedBytes),
		zap.Uint64("available_bytes", availableBytes),
		zap.Uint64("total_inodes", totalInodes),
		zap.Uint64("used_inodes", usedInodes),
		zap.Int64("db_size_bytes", dbSizeBytes),
	)

	return nil
}

// evaluatePressure sets the storage pressure gauges and logs warnings based
// on the current usage percentage and the configured thresholds.
func (m *Monitor) evaluatePressure(usagePercent float64) {
	warningThreshold := float64(m.cfg.Storage.WarningThreshold)
	criticalThreshold := float64(m.cfg.Storage.CriticalThreshold)

	// Reset pressure gauges.
	m.metrics.StoragePressure.WithLabelValues("none").Set(0)
	m.metrics.StoragePressure.WithLabelValues("warning").Set(0)
	m.metrics.StoragePressure.WithLabelValues("critical").Set(0)

	switch {
	case usagePercent >= criticalThreshold:
		m.metrics.StoragePressure.WithLabelValues("critical").Set(1)
		m.logger.Error("CRITICAL: storage usage exceeds critical threshold",
			zap.Float64("usage_percent", usagePercent),
			zap.Float64("critical_threshold", criticalThreshold),
		)
	case usagePercent >= warningThreshold:
		m.metrics.StoragePressure.WithLabelValues("warning").Set(1)
		m.logger.Warn("storage usage exceeds warning threshold",
			zap.Float64("usage_percent", usagePercent),
			zap.Float64("warning_threshold", warningThreshold),
		)
	default:
		m.metrics.StoragePressure.WithLabelValues("none").Set(1)
	}
}
