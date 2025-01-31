// Copyright 2024 Canonical.

package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/servermon"
)

// AddCloud adds the given cloud to the database. AddCloud returns an error
// with a code of CodeAlreadyExists if there is already a cloud with the
// same name.
func (d *Database) AddCloud(ctx context.Context, c *dbmodel.Cloud) (err error) {
	const op = errors.Op("db.AddCloud")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Create(c).Error; err != nil {
		err := dbError(err)
		if errors.ErrorCode(err) == errors.CodeAlreadyExists {
			return errors.E(op, fmt.Sprintf("cloud %q already exists", c.Name), err)
		}
		return errors.E(op, err)
	}
	return nil
}

// GetCloud fills in the given cloud document based on the cloud name. If
// no cloud is found with the matching name then an error with a code of
// CodeNotFound will be returned.
func (d *Database) GetCloud(ctx context.Context, c *dbmodel.Cloud) (err error) {
	const op = errors.Op("db.GetCloud")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	db = db.Where("name = ?", c.Name)
	db = preloadCloud("", db)
	if err := db.First(&c).Error; err != nil {
		err := dbError(err)
		if errors.ErrorCode(err) == errors.CodeNotFound {
			return errors.E(op, fmt.Sprintf("cloud %q not found", c.Name), err)
		}
		return errors.E(op, err)
	}
	return nil
}

// GetClouds retrieves all the clouds from the database.
func (d *Database) GetClouds(ctx context.Context) (_ []dbmodel.Cloud, err error) {
	const op = errors.Op("db.GetClouds")
	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	var clouds []dbmodel.Cloud
	db := d.DB.WithContext(ctx)
	db = preloadCloud("", db)
	if err := db.Find(&clouds).Error; err != nil {
		return nil, errors.E(op, err)
	}
	return clouds, nil
}

// UpdateCloud updates the database definition of the cloud to match the
// given cloud. UpdateCloud does not update any user information, nor does
// it remove any information - this is an additive method.
func (d *Database) UpdateCloud(ctx context.Context, c *dbmodel.Cloud) (err error) {
	const op = errors.Op("db.UpdateCloud")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	err = d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(c).Error; err != nil {
			return err
		}
		for _, r := range c.Regions {
			r.CloudName = c.Name
			if err := tx.Save(&r).Error; err != nil {
				return err
			}
			for _, ctl := range r.Controllers {
				ctl.CloudRegionID = r.ID
				if err := tx.Save(&ctl).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

func preloadCloud(prefix string, db *gorm.DB) *gorm.DB {
	if len(prefix) > 0 && prefix[len(prefix)-1] != '.' {
		prefix += "."
	}
	db = db.Preload(prefix + "Regions").Preload(prefix + "Regions.Controllers").Preload(prefix + "Regions.Controllers.Controller")
	return db
}

// AddCloudRegion adds a new cloud-region to a cloud. AddCloudRegion
// returns an error with a code of CodeAlreadyExists if there is already a
// region with the same name on the cloud.
func (d *Database) AddCloudRegion(ctx context.Context, cr *dbmodel.CloudRegion) (err error) {
	const op = errors.Op("db.AddCloudRegion")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Create(cr).Error; err != nil {
		err := dbError(err)
		if errors.ErrorCode(err) == errors.CodeAlreadyExists {
			return errors.E(op, fmt.Sprintf("cloud-region %s/%s already exists", cr.CloudName, cr.Name), err)
		}
		return errors.E(op, err)
	}
	return nil
}

// FindRegionByCloudType finds a region with the given name on a cloud with the given
// cloud type.
func (d *Database) FindRegionByCloudType(ctx context.Context, providerType, regionName string) (_ *dbmodel.CloudRegion, err error) {
	const op = errors.Op("db.FindRegion")
	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	db = db.Preload("Cloud").Preload("Controllers").Preload("Controllers.Controller")
	db = db.Model(dbmodel.CloudRegion{}).Joins("INNER JOIN clouds ON clouds.name = cloud_regions.cloud_name").Where("clouds.type = ? AND cloud_regions.name = ?", providerType, regionName)

	var region dbmodel.CloudRegion
	if err := db.First(&region).Error; err != nil {
		return nil, errors.E(op, dbError(err))
	}
	return &region, nil
}

// FindRegionByCloudName finds a region with the given name on a cloud with the given
// name.
func (d *Database) FindRegionByCloudName(ctx context.Context, cloudName, regionName string) (_ *dbmodel.CloudRegion, err error) {
	const op = errors.Op("db.FindRegion")
	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	db = db.Preload("Cloud").Preload("Controllers").Preload("Controllers.Controller")
	db = db.Model(dbmodel.CloudRegion{}).Joins("INNER JOIN clouds ON clouds.name = cloud_regions.cloud_name").Where("clouds.name = ? AND cloud_regions.name = ?", cloudName, regionName)

	var region dbmodel.CloudRegion
	if err := db.First(&region).Error; err != nil {
		return nil, errors.E(op, dbError(err))
	}
	return &region, nil
}

// DeleteCloud deletes the given cloud.
func (d *Database) DeleteCloud(ctx context.Context, c *dbmodel.Cloud) (err error) {
	const op = errors.Op("db.DeleteCloud")

	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Delete(c).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// DeleteCloudRegionControllerPriority deletes the given cloud region controller priority entry.
func (d *Database) DeleteCloudRegionControllerPriority(ctx context.Context, c *dbmodel.CloudRegionControllerPriority) (err error) {
	const op = errors.Op("db.DeleteCloudRegionControllerPriority")

	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Delete(c).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}
