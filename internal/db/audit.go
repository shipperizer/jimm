// Copyright 2024 Canonical.

package db

import (
	"context"
	"time"

	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/servermon"
)

// AddAuditLogEntry adds a new entry to the audit log.
func (d *Database) AddAuditLogEntry(ctx context.Context, ale *dbmodel.AuditLogEntry) (err error) {
	const op = errors.Op("db.AddAuditLogEntry")

	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	if err := d.DB.WithContext(ctx).Create(ale).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// An AuditLogFilter defines a filter for audit-log entries.
type AuditLogFilter struct {
	// Start defines the earliest time to show audit events for. If
	// this is zero then all audit events that are before the End time
	// are found.
	Start time.Time

	// End defines the latest time to show audit events for. If this is
	// zero then all audit events that are after the Start time are
	// found.
	End time.Time

	// IdentityTag defines the identity-tag on the audit log entry to match, if
	// this is empty all identity-tags are matched.
	IdentityTag string

	// Model is used to filter the event log to only contain events that
	// were performed against a specific model.
	Model string `json:"model,omitempty"`

	// Method is used to filter the event log to only contain events that
	// called a specific facade method.
	Method string `json:"method,omitempty"`

	// Offset is an offset that will be added when retrieving audit logs.
	// An empty offset is equivalent to zero.
	Offset int `json:"offset,omitempty"`

	// Limit is the maximum number of audit events to return.
	// A value of zero will ignore the limit.
	Limit int `json:"limit,omitempty"`

	// SortTime will sort by most recent first (time descending) when true.
	// When false no explicit ordering will be applied.
	SortTime bool `json:"sortTime,omitempty"`
}

// ForEachAuditLogEntry iterates through all audit log entries that match
// the given filter calling f for each entry. If f returns an error
// iteration stops immediately and the error is retuned unmodified.
func (d *Database) ForEachAuditLogEntry(ctx context.Context, filter AuditLogFilter, f func(*dbmodel.AuditLogEntry) error) (err error) {
	const op = errors.Op("db.ForEachAuditLogEntry")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx).Model(&dbmodel.AuditLogEntry{})
	if !filter.Start.IsZero() {
		db = db.Where("time >= ?", filter.Start)
	}
	if !filter.End.IsZero() {
		db = db.Where("time <= ?", filter.End)
	}
	if filter.IdentityTag != "" {
		db = db.Where("identity_tag = ?", filter.IdentityTag)
	}
	if filter.Model != "" {
		db = db.Where("model = ?", filter.Model)
	}
	if filter.Method != "" {
		db = db.Where("facade_method = ?", filter.Method)
	}
	if filter.SortTime {
		db = db.Order("time DESC")
	}
	if filter.Limit > 0 {
		db = db.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		db = db.Offset(filter.Offset)
	}

	rows, err := db.Rows()
	if err != nil {
		return errors.E(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ale dbmodel.AuditLogEntry
		if err := db.ScanRows(rows, &ale); err != nil {
			return errors.E(op, err)
		}
		if err := f(&ale); err != nil {
			return err
		}
	}
	if rows.Err() != nil {
		return errors.E(op, rows.Err())
	}
	return nil
}

// CleanupAuditLogs cleans up audit logs after the auditLogRetentionPeriodInDays,
// HARD deleting them from the database.
func (d *Database) DeleteAuditLogsBefore(ctx context.Context, before time.Time) (_ int64, err error) {
	const op = errors.Op("db.DeleteAuditLogsBefore")

	if err := d.ready(); err != nil {
		return 0, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	tx := d.DB.
		WithContext(ctx).
		Unscoped().
		Where("time < ?", before).
		Delete(&dbmodel.AuditLogEntry{})
	if tx.Error != nil {
		return 0, errors.E(op, dbError(tx.Error))
	}
	return tx.RowsAffected, nil
}
