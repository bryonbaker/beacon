// Package cleaner implements the periodic cleanup loop that removes old,
// fully-notified deleted records from the database to prevent unbounded
// growth of the managed_objects table.
package cleaner

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/bryonbaker/beacon/internal/config"
	"github.com/bryonbaker/beacon/internal/database"
	"github.com/bryonbaker/beacon/internal/metrics"
)

// Cleaner periodically removes database records that have been in the deleted
// state long enough and whose notifications have been successfully sent.
type Cleaner struct {
	db      database.Database
	cfg     *config.Config
	metrics *metrics.Metrics
	logger  *zap.Logger
}

// NewCleaner creates a new Cleaner with the provided dependencies.
func NewCleaner(db database.Database, cfg *config.Config, m *metrics.Metrics, logger *zap.Logger) *Cleaner {
	return &Cleaner{
		db:      db,
		cfg:     cfg,
		metrics: m,
		logger:  logger,
	}
}

// Start begins the cleanup loop, running at the configured cleanup interval.
// The loop stops when ctx is cancelled.
func (c *Cleaner) Start(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.Retention.CleanupInterval.Duration)
	defer ticker.Stop()

	c.logger.Info("cleaner started",
		zap.Duration("cleanup_interval", c.cfg.Retention.CleanupInterval.Duration),
		zap.Duration("retention_period", c.cfg.Retention.RetentionPeriod.Duration),
	)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("cleaner stopping", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			if err := c.Cleanup(ctx); err != nil {
				c.logger.Error("cleanup failed", zap.Error(err))
			}
		}
	}
}

// Cleanup performs a single cleanup pass. It queries for records that are
// eligible for deletion (deleted, fully notified, and older than the retention
// period), removes them, runs an incremental vacuum to reclaim space, and
// updates metrics.
func (c *Cleaner) Cleanup(ctx context.Context) error {
	start := time.Now()

	// Query records eligible for cleanup.
	eligible, err := c.db.GetCleanupEligible(c.cfg.Retention.RetentionPeriod.Duration)
	if err != nil {
		c.metrics.CleanupRunsTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("querying cleanup-eligible records: %w", err)
	}

	c.metrics.CleanupRecordsEligible.Set(float64(len(eligible)))

	if len(eligible) == 0 {
		c.logger.Debug("no records eligible for cleanup")
		c.metrics.CleanupRunsTotal.WithLabelValues("success").Inc()
		c.metrics.CleanupDuration.Observe(time.Since(start).Seconds())
		return nil
	}

	// Track the oldest record age for metrics.
	var oldestAge time.Duration
	for _, obj := range eligible {
		if obj.DeletedAt != nil {
			age := time.Since(*obj.DeletedAt)
			if age > oldestAge {
				oldestAge = age
			}
		}
	}
	c.metrics.CleanupOldestRecordAge.Set(oldestAge.Seconds())

	// Delete each eligible record.
	deleted := 0
	for _, obj := range eligible {
		select {
		case <-ctx.Done():
			c.logger.Info("cleanup interrupted by context cancellation",
				zap.Int("deleted_so_far", deleted),
			)
			c.metrics.CleanupRunsTotal.WithLabelValues("interrupted").Inc()
			return ctx.Err()
		default:
		}

		if err := c.db.DeleteRecord(obj.ID); err != nil {
			c.logger.Error("failed to delete record",
				zap.String("id", obj.ID),
				zap.String("resource_uid", obj.ResourceUID),
				zap.Error(err),
			)
			continue
		}
		deleted++
	}

	c.metrics.CleanupRecordsDeleted.Add(float64(deleted))

	// Run incremental vacuum to reclaim disk space.
	if err := c.db.RunIncrementalVacuum(); err != nil {
		c.logger.Error("incremental vacuum failed", zap.Error(err))
		// Not a fatal error; cleanup was still successful.
	}

	duration := time.Since(start)
	c.metrics.CleanupDuration.Observe(duration.Seconds())
	c.metrics.CleanupRunsTotal.WithLabelValues("success").Inc()

	c.logger.Info("cleanup completed",
		zap.Int("eligible", len(eligible)),
		zap.Int("deleted", deleted),
		zap.Duration("duration", duration),
	)

	return nil
}
