// Copyright 2024 Canonical.

package db

import (
	"context"

	"gorm.io/gorm"

	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/servermon"
)

// AddModel stores the model information.
//   - returns an error with code errors.CodeAlreadyExists if
//     model with the same name already exists.
func (d *Database) AddModel(ctx context.Context, model *dbmodel.Model) (err error) {
	const op = errors.Op("db.AddModel")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)

	if err := db.Create(model).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// GetModel returns model information based on the
// model UUID.
func (d *Database) GetModel(ctx context.Context, model *dbmodel.Model) (err error) {
	const op = errors.Op("db.GetModel")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	switch {
	case model.UUID.Valid:
		db = db.Where("uuid = ?", model.UUID.String)
		if model.ControllerID != 0 {
			db = db.Where("controller_id = ?", model.ControllerID)
		}
	case model.ID != 0:
		db = db.Where("id = ?", model.ID)
	case model.OwnerIdentityName != "" && model.Name != "":
		db = db.Where("owner_identity_name = ? AND name = ?", model.OwnerIdentityName, model.Name)
	case model.ControllerID != 0:
		// TODO: fix ordering of where fields and handle error to represent what is *actually* required.
		db = db.Where("controller_id = ?", model.ControllerID)
	default:
		return errors.E(op, "missing id or uuid", errors.CodeBadRequest)
	}

	db = preloadModel("", db)

	if err := db.First(&model).Error; err != nil {
		err = dbError(err)
		if errors.ErrorCode(err) == errors.CodeNotFound {
			return errors.E(op, err, "model not found")
		}
		return errors.E(op, dbError(err))
	}
	return nil
}

// GetModelsUsingCredential returns all models that use the specified credentials.
func (d *Database) GetModelsUsingCredential(ctx context.Context, credentialID uint) (_ []dbmodel.Model, err error) {
	const op = errors.Op("db.GetModelsUsingCredential")
	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	var models []dbmodel.Model
	result := db.Where("cloud_credential_id = ?", credentialID).Preload("Controller").Find(&models)
	if result.Error != nil {
		return nil, errors.E(op, dbError(result.Error))
	}
	return models, nil
}

// UpdateModel updates the model information.
func (d *Database) UpdateModel(ctx context.Context, model *dbmodel.Model) (err error) {
	const op = errors.Op("db.UpdateModel")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Save(model).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// DeleteModel removes the model information from the database.
func (d *Database) DeleteModel(ctx context.Context, model *dbmodel.Model) (err error) {
	const op = errors.Op("db.DeleteModel")
	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	if err := db.Delete(model, model.ID).Error; err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// ForEachModel iterates through every model calling the given function
// for each one. If the given function returns an error the iteration
// will stop immediately and the error will be returned unmodified.
func (d *Database) ForEachModel(ctx context.Context, f func(m *dbmodel.Model) error) (err error) {
	const op = errors.Op("db.ForEachModel")

	if err := d.ready(); err != nil {
		return errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	db := d.DB.WithContext(ctx)
	db = preloadModel("", db)
	rows, err := db.Model(&dbmodel.Model{}).Rows()
	if err != nil {
		return errors.E(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		var m dbmodel.Model
		if err := db.ScanRows(rows, &m); err != nil {
			return errors.E(op, err)
		}
		// ScanRows does not use the preloads added on L141, therefore
		// we need to fetch each model to load the associated
		// fields otherwise the only populated fields will be association
		// IDs.
		if err := d.GetModel(ctx, &m); err != nil {
			return errors.E(op, err)
		}
		if err := f(&m); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return errors.E(op, dbError(err))
	}
	return nil
}

// GetModelsByUUID retrieves a list of models where the model UUIDs are in
// the provided modelUUIDs slice.
//
// If the UUID cannot be resolved to a model, it is skipped from the result and
// no error is returned.
func (d *Database) GetModelsByUUID(ctx context.Context, modelUUIDs []string) (_ []dbmodel.Model, err error) {
	const op = errors.Op("db.GetModelsByUUID")

	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}

	durationObserver := servermon.DurationObserver(servermon.DBQueryDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.DBQueryErrorCount, &err, string(op))

	var models []dbmodel.Model
	db := d.DB.WithContext(ctx)
	db = preloadModel("", db)
	err = db.Where("uuid IN ?", modelUUIDs).Find(&models).Error
	if err != nil {
		err = dbError(err)
		if errors.ErrorCode(err) == errors.CodeNotFound {
			return nil, errors.E(op, err, "model not found")
		}
		return nil, errors.E(op, dbError(err))
	}
	return models, nil
}

func preloadModel(prefix string, db *gorm.DB) *gorm.DB {
	if len(prefix) > 0 && prefix[len(prefix)-1] != '.' {
		prefix += "."
	}
	db = db.Preload(prefix + "Owner")
	db = db.Preload(prefix + "Controller")
	db = db.Preload(prefix + "CloudRegion").Preload(prefix + "CloudRegion.Cloud")
	// We don't care about the cloud credential owner when
	// loading a model, as we just use the credential to deploy
	// applications.
	db = db.Preload(prefix + "CloudCredential")
	db = db.Preload(prefix + "Offers")

	return db
}

// GetModelsByController retrieves a list of models hosted on the specified controller.
// Note that because we do not preload here, foreign key references will be empty.
func (d *Database) GetModelsByController(ctx context.Context, ctl dbmodel.Controller) ([]dbmodel.Model, error) {
	const op = errors.Op("db.GetModelsByController")

	if err := d.ready(); err != nil {
		return nil, errors.E(op, err)
	}
	var models []dbmodel.Model
	db := d.DB.WithContext(ctx)
	if err := db.Model(ctl).Association("Models").Find(&models); err != nil {
		return nil, errors.E(op, dbError(err))
	}
	return models, nil
}

// CountModelsByController counts the number of models hosted on a controller.
func (d *Database) CountModelsByController(ctx context.Context, ctl dbmodel.Controller) (int, error) {
	const op = errors.Op("db.CountModelsByController")

	if err := d.ready(); err != nil {
		return 0, errors.E(op, err)
	}
	db := d.DB.WithContext(ctx)
	asc := db.Model(ctl).Association("Models")
	count := asc.Count()
	if err := asc.Error; err != nil {
		return 0, errors.E(op, dbError(err))
	}
	return int(count), nil
}
