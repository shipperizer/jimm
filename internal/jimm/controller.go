// Copyright 2024 Canonical.

package jimm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/juju/juju/api/base"
	"github.com/juju/juju/api/controller/controller"
	jujuparams "github.com/juju/juju/rpc/params"
	"github.com/juju/names/v5"
	"github.com/juju/version"
	"github.com/juju/zaputil"
	"github.com/juju/zaputil/zapctx"
	"go.uber.org/zap"
	"gopkg.in/macaroon.v2"

	"github.com/canonical/jimm/v3/internal/db"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/openfga"
	ofganames "github.com/canonical/jimm/v3/internal/openfga/names"
)

// ControllerClient defines an interface of the juju controller api client
// used by JIMM to interact with the Controller facade of Juju controllers.
type ControllerClient interface {
	// InitiateMigration attempts to begin the migration of one or
	// more models to other controllers.
	InitiateMigration(controller.MigrationSpec) (string, error)
	// Close closes the connection to the API server.
	Close() error
}

var (
	newControllerClient = func(api base.APICallCloser) ControllerClient {
		return controller.NewClient(api)
	}
)

// convertJujuCloudsToDbClouds converts all of the incoming Juju clouds (from a map) into
// a slice of dbmodel Clouds.
func convertJujuCloudsToDbClouds(clouds map[names.CloudTag]jujuparams.Cloud) []dbmodel.Cloud {
	var dbClouds []dbmodel.Cloud
	for tag, cld := range clouds {
		var cloud dbmodel.Cloud
		cloud.FromJujuCloud(cld)
		cloud.Name = tag.Id()
		dbClouds = append(dbClouds, cloud)
	}
	return dbClouds
}

// getControllerModelSummary returns the controllers model summary.
func getControllerModelSummary(ctx context.Context, api API) (jujuparams.ModelSummary, error) {
	var ms jujuparams.ModelSummary
	if err := api.ControllerModelSummary(ctx, &ms); err != nil {
		zapctx.Error(ctx, "failed to get model summary", zaputil.Error(err))
		return ms, err
	}
	return ms, nil
}

// getCloudNameFromModelSummary returns the cloud name for a model summary.
func getCloudNameFromModelSummary(modelSummary jujuparams.ModelSummary) (string, error) {
	cloudTag, err := names.ParseCloudTag(modelSummary.CloudTag)
	if err != nil {
		return "", err
	}
	return cloudTag.Id(), nil
}

// addControllerTransactor adds a controller to the database ensuring it's clouds, regions
// and region priorities have also been persisted. Additionally, it ensures the region
// priorities are set too.
type addControllerTransactor struct {
	jimm       *JIMM
	jujuClouds []dbmodel.Cloud
	controller *dbmodel.Controller
	tx         *db.Database
}

// newAddControllerTransactor creates a new addControllerTransactor.
func newAddControllerTransactor(j *JIMM, jujuClouds []dbmodel.Cloud, ctl *dbmodel.Controller, tx *db.Database) *addControllerTransactor {
	return &addControllerTransactor{
		jimm:       j,
		jujuClouds: jujuClouds,
		controller: ctl,
		tx:         tx,
	}
}

// addCloud adds a cloud from a juju API call within a transaction.
//
// After the cloud has been added, it is returned.
func (act *addControllerTransactor) addCloud(ctx context.Context, jujuCloud dbmodel.Cloud) (dbmodel.Cloud, error) {
	cloud := jujuCloud
	if err := act.tx.GetCloud(ctx, &cloud); err != nil {
		if errors.ErrorCode(err) != errors.CodeNotFound {
			zapctx.Error(ctx, "failed to fetch the cloud", zaputil.Error(err), zap.String("cloud-name", jujuCloud.Name))
			return cloud, err
		}
		err := act.tx.AddCloud(ctx, &cloud)
		if err != nil && errors.ErrorCode(err) != errors.CodeAlreadyExists {
			zapctx.Error(ctx, "failed to add cloud", zaputil.Error(err))
			return cloud, err
		}
	}
	return cloud, nil
}

// addCloudRegions iterates over the regions for the passed cloud, adding them to the database in the
// existing transaction.
//
// Additionally, it appends the added cloud region (to the database) to the passed
// cloud dbmodel.Cloud. This prevents the need to get the cloud from the database again.
func (act *addControllerTransactor) addCloudRegions(ctx context.Context, cloud dbmodel.Cloud, regions []dbmodel.CloudRegion) (dbmodel.Cloud, error) {
	for _, reg := range regions {
		if cloud.Region(reg.Name).ID != 0 {
			continue
		}
		reg.CloudName = cloud.Name
		if err := act.tx.AddCloudRegion(ctx, &reg); err != nil {
			zapctx.Error(ctx, "failed to add cloud region", zaputil.Error(err))
			return cloud, err
		}
	}
	return cloud, nil
}

// Sets controller cloud region priorities for this dbmodel.Controller,
// these priorities are set based on the following.
//
// Regions are defined on two fields, the cloud name and the region name.
//
// We have two priorities and they are set based on whether
// the incoming region matches the controllers model region.
//
//  1. Priority supported:
//     If the region is NOT the same as the controllers region,
//     it holds this priority.
//  2. Priority deployed:
//     If the region is the same as the controller model,
//     it holds this priority.
//
// It is expected that the cloud passed has already been loaded with the previously added
// regions. These regions will be appended to the controller's cloud region priorities.
// in preparation for adding the controller.
func (act *addControllerTransactor) setCloudRegionControllerPriorities(cloud dbmodel.Cloud, regions []dbmodel.CloudRegion) {
	for _, cr := range regions {
		reg := cloud.Region(cr.Name)

		priority := dbmodel.CloudRegionControllerPrioritySupported

		if cloud.Name == act.controller.CloudName && cr.Name == act.controller.CloudRegion {
			priority = dbmodel.CloudRegionControllerPriorityDeployed
		}

		act.controller.CloudRegions = append(act.controller.CloudRegions, dbmodel.CloudRegionControllerPriority{
			CloudRegion: reg,
			//nolint:gosec
			Priority: uint(priority),
		})
	}
}

// Run runs the transactor to add a controller to JIMM.
func (act *addControllerTransactor) Run(ctx context.Context) error {
	// Add clouds and their regions to db and sets the controllers
	// cloud region priorities
	for i := range act.jujuClouds {
		incomingJujuCloud := act.jujuClouds[i]

		// Add the cloud
		addedCloud, err := act.addCloud(ctx, incomingJujuCloud)
		if err != nil {
			return err
		}

		// Add the clouds regions
		_, err = act.addCloudRegions(ctx, addedCloud, incomingJujuCloud.Regions)
		if err != nil {
			return err
		}
		// Get the cloud again to populate it's regions (regions are preloaded)
		// and now they can be used for updating the controller's region priorities.
		if err := act.tx.GetCloud(ctx, &addedCloud); err != nil {
			return err
		}

		// Update controller dbmodel's region priotiries
		act.setCloudRegionControllerPriorities(addedCloud, act.jujuClouds[i].Regions)
	}

	// Finally, add the controller with all clouds and their regions set
	if err := act.tx.AddController(ctx, act.controller); err != nil {
		return err
	}
	return nil
}

// addControllerTx stores the clouds, regions, cloud region priorities and the controller itself in the database determined
// from the incoming Juju API.Clouds() call.
func addControllerTx(ctx context.Context, j *JIMM, jujuClouds []dbmodel.Cloud, ctl *dbmodel.Controller) error {
	return j.Database.Transaction(func(tx *db.Database) error {
		return newAddControllerTransactor(j, jujuClouds, ctl, tx).Run(ctx)
	})
}

// AddController adds the specified controller to JIMM. Only
// controller-admin level users may add new controllers. If the user adding
// the controller is not authorized then an error with a code of
// CodeUnauthorized will be returned. If there already exists a controller
// with the same name as the controller being added then an error with a
// code of CodeAlreadyExists will be returned. If the controller cannot be
// contacted then an error with a code of CodeConnectionFailed will be
// returned.
func (j *JIMM) AddController(ctx context.Context, user *openfga.User, ctl *dbmodel.Controller) error {
	const op = errors.Op("jimm.AddController")

	if err := j.checkJimmAdmin(user); err != nil {
		return err
	}

	api, err := j.dialController(ctx, ctl)
	if err != nil {
		return errors.E(op, "failed to dial the controller", err)
	}
	defer api.Close()

	modelSummary, err := getControllerModelSummary(ctx, api)
	if err != nil {
		return errors.E(op, err, "failed to get model summary")
	}

	cloudName, err := getCloudNameFromModelSummary(modelSummary)
	if err != nil {
		return errors.E(op, err, "failed to parse the cloud tag")
	}

	ctl.CloudName = cloudName
	ctl.CloudRegion = modelSummary.CloudRegion
	// TODO(mhilton) add the controller model?

	clouds, err := api.Clouds(ctx)
	if err != nil {
		return errors.E(op, err, "failed to fetch controller clouds")
	}

	dbClouds := convertJujuCloudsToDbClouds(clouds)

	// TODO(ale8k): This shouldn't be necessary to check, but tests need updating
	// to set insecure credential store explicitly.
	if j.CredentialStore != nil {
		err := j.CredentialStore.PutControllerCredentials(ctx, ctl.Name, ctl.AdminIdentityName, ctl.AdminPassword)
		if err != nil {
			return errors.E(op, err, "failed to store controller credentials")
		}
	}

	// Credential store will always be set either to vault or explicitly insecure,
	// no need to be persist in db.
	ctl.AdminIdentityName = ""
	ctl.AdminPassword = ""

	if err := addControllerTx(ctx, j, dbClouds, ctl); err != nil {
		zapctx.Error(ctx, "failed to add controller", zaputil.Error(err))
		if errors.ErrorCode(err) == errors.CodeAlreadyExists {
			return errors.E(op, err, fmt.Sprintf("controller %q already exists", ctl.Name))
		}

		return errors.E(op, err)
	}

	for _, cloud := range dbClouds {
		// If this cloud is the one used by the controller model then
		// it is available to all users. Other clouds require `juju grant-cloud` to add permissions.
		if cloud.ResourceTag().String() == modelSummary.CloudTag {
			if err := j.everyoneUser().SetCloudAccess(ctx, cloud.ResourceTag(), ofganames.CanAddModelRelation); err != nil {
				zapctx.Error(ctx, "failed to grant everyone add-model access", zap.Error(err))
			}
		}

		// Add controller relation between the cloud and the added controller.
		err = j.OpenFGAClient.AddCloudController(ctx, cloud.ResourceTag(), ctl.ResourceTag())
		if err != nil {
			zapctx.Error(
				ctx,
				"failed to add controller relation between controller and cloud",
				zap.String("controller", ctl.ResourceTag().Id()),
				zap.String("cloud", cloud.ResourceTag().Id()),
				zap.Error(err),
			)
		}
	}

	// Finally add a controller relation between JIMM and the added controller.
	err = j.OpenFGAClient.AddController(ctx, j.ResourceTag(), ctl.ResourceTag())
	if err != nil {
		zapctx.Error(
			ctx,
			"failed to add controller relation between JIMM and controller",
			zap.String("controller", ctl.ResourceTag().Id()),
			zap.Error(err),
		)
	}

	return nil
}

// EarliestControllerVersion returns the earliest agent version
// that any of the available public controllers is known to be running.
// If there are no available controllers or none of their versions are
// known, it returns the zero version.
func (j *JIMM) EarliestControllerVersion(ctx context.Context) (version.Number, error) {
	const op = errors.Op("jimm.EarliestControllerVersion")
	var v *version.Number

	err := j.Database.ForEachController(ctx, func(controller *dbmodel.Controller) error {
		if controller.AgentVersion == "" {
			return nil
		}
		versionNumber, err := version.Parse(controller.AgentVersion)
		if err != nil {
			zapctx.Error(
				ctx,
				"failed to parse agent version",
				zap.String("version", controller.AgentVersion),
				zap.String("controller", controller.Name),
			)
			//nolint:nilerr // We wish to log without an error returned, TODO: Check with Ales
			return nil
		}
		if v == nil || versionNumber.Compare(*v) < 0 {
			v = &versionNumber
		}
		return nil
	})
	if err != nil {
		return version.Number{}, errors.E(op, err)
	}
	if v == nil {
		return version.Number{}, nil
	}
	return *v, nil
}

// GetJimmControllerAccess returns the JIMM controller access level for the
// requested user.
func (j *JIMM) GetJimmControllerAccess(ctx context.Context, user *openfga.User, tag names.UserTag) (string, error) {
	const op = errors.Op("jimm.GetJIMMControllerAccess")

	// If the authenticated user is requesting the access level
	// for him/her-self then we return that - either the user
	// is a JIMM admin (aka "superuser"), or they have a "login"
	// access level.
	if user.Name == tag.Id() {
		if user.JimmAdmin {
			return "superuser", nil
		}
		return "login", nil
	}

	// Only JIMM administrators are allowed to see the access
	// level of somebody else.
	if !user.JimmAdmin {
		return "", errors.E(op, errors.CodeUnauthorized, "unauthorized")
	}

	var targetUser dbmodel.Identity
	targetUser.SetTag(tag)
	targetUserTag := openfga.NewUser(&targetUser, j.OpenFGAClient)

	// Check if the user is jimm administrator.
	isAdmin, err := openfga.IsAdministrator(ctx, targetUserTag, j.ResourceTag())
	if err != nil {
		zapctx.Error(ctx, "failed to check access rights", zap.Error(err))
		return "", errors.E(op, err)
	}
	if isAdmin {
		return "superuser", nil
	}

	return "login", nil
}

// GetUserControllerAccess returns the user's level of access to the desired controller.
func (j *JIMM) GetUserControllerAccess(ctx context.Context, user *openfga.User, controller names.ControllerTag) (string, error) {
	accessLevel := user.GetControllerAccess(ctx, controller)
	return ToControllerAccessString(accessLevel), nil
}

type modelImporter struct {
	jimm      *JIMM
	model     dbmodel.Model
	modelInfo jujuparams.ModelInfo
	// newOwner may be nil if the user wants to keep the original owner.
	newOwner      *names.UserTag
	originalOwner names.UserTag
	offersToAdd   []jujuparams.ApplicationOfferAdminDetailsV5
}

func newModelImporter(jimm *JIMM, newOwner string) (modelImporter, error) {
	modelImporter := modelImporter{
		jimm: jimm,
	}
	if newOwner == "" {
		return modelImporter, nil
	}
	if !names.IsValidUser(newOwner) {
		return modelImporter, errors.E(errors.CodeBadRequest, "invalid new username for new model owner")
	}
	newOwnerTag := names.NewUserTag(newOwner)
	modelImporter.newOwner = &newOwnerTag
	return modelImporter, nil
}

func (m *modelImporter) fetchModelInfo(ctx context.Context, controllerName string, modelTag names.ModelTag) error {
	controller, err := m.jimm.getControllerByName(ctx, controllerName)
	if err != nil {
		return err
	}

	api, err := m.jimm.dialController(ctx, controller)
	if err != nil {
		return errors.E("failed to dial the controller", err)
	}
	defer api.Close()

	m.modelInfo = jujuparams.ModelInfo{
		UUID: modelTag.Id(),
	}
	err = api.ModelInfo(ctx, &m.modelInfo)
	if err != nil {
		return err
	}

	m.originalOwner, err = names.ParseUserTag(m.modelInfo.OwnerTag)
	if err != nil {
		return errors.E(fmt.Sprintf("invalid username %s from original model owner", m.modelInfo.OwnerTag))
	}

	m.offersToAdd, err = api.ListApplicationOffers(ctx, []jujuparams.OfferFilter{
		{
			OwnerName: m.originalOwner.Id(),
			ModelName: m.modelInfo.Name,
		},
	})
	if err != nil {
		return err
	}

	// fill in data from model info
	err = m.model.FromJujuModelInfo(m.modelInfo)
	if err != nil {
		return err
	}
	m.model.ControllerID = controller.ID
	m.model.Controller = *controller

	return nil
}

func (m *modelImporter) setModelOwner(ctx context.Context) error {
	var ownerTag names.UserTag
	if m.newOwner != nil {
		ownerTag = *m.newOwner
	} else {
		ownerTag = m.originalOwner
	}

	if ownerTag.IsLocal() {
		return errors.E("cannot import model from local user, try --owner to switch the model owner")
	}
	owner := dbmodel.Identity{}
	owner.SetTag(ownerTag)

	err := m.jimm.Database.GetIdentity(ctx, &owner)
	if err != nil {
		return errors.E(err)
	}
	m.model.SetOwner(&owner)

	return nil
}

// addPermissions grants the model owner with admin access to the model
// and, in turn, admin access to any offers within the model.
func (m *modelImporter) addPermissions(ctx context.Context) error {
	// Note that only the new owner is given access. All previous users that had access according to Juju
	// are discarded as access must now be governed by JIMM and OpenFGA.
	ofgaUser := openfga.NewUser(&m.model.Owner, m.jimm.OpenFGAClient)
	controllerTag := m.model.Controller.ResourceTag()

	if err := m.jimm.addModelPermissions(ctx, ofgaUser, m.model.ResourceTag(), controllerTag); err != nil {
		return err
	}

	for _, offer := range m.offersToAdd {
		err := m.jimm.OpenFGAClient.AddModelApplicationOffer(ctx, m.model.ResourceTag(), names.NewApplicationOfferTag(offer.OfferUUID))
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *modelImporter) setCloudCredential(ctx context.Context) error {
	// fetch cloud credential used by the model
	cloudTag, err := names.ParseCloudTag(m.modelInfo.CloudTag)
	if err != nil {
		return err
	}

	// Note that the model already has a cloud credential configured which it will use when deploying new
	// applications. JIMM needs some cloud credential reference to be able to import the model so use any
	// credential against the cloud the model is deployed against. Even using the correct cloud for the
	// credential is not strictly necessary, but will help prevent the user thinking they can create new
	// models on the incoming cloud.
	allCredentials, err := m.jimm.Database.GetIdentityCloudCredentials(ctx, &m.model.Owner, cloudTag.Id())
	if err != nil {
		return err
	}
	if len(allCredentials) == 0 {
		return errors.E(errors.CodeNotFound, fmt.Sprintf("Failed to find cloud credential for user %s on cloud %s", m.model.Owner.Name, cloudTag.Id()))
	}
	cloudCredential := allCredentials[0]

	m.model.CloudCredentialID = cloudCredential.ID
	m.model.CloudCredential = cloudCredential

	return nil
}

func (m *modelImporter) setModelCloud(ctx context.Context) error {
	// fetch the cloud used by the model
	cloudTag, err := names.ParseCloudTag(m.modelInfo.CloudTag)
	if err != nil {
		return err
	}
	cloud := dbmodel.Cloud{Name: cloudTag.Id()}
	err = m.jimm.Database.GetCloud(ctx, &cloud)
	if err != nil {
		zapctx.Error(ctx, "failed to get cloud", zap.String("cloud", cloud.Name))
		return err
	}

	cr := cloud.Region(m.modelInfo.CloudRegion)
	if cr.Name != m.modelInfo.CloudRegion {
		return errors.E("cloud region not found")
	}

	m.model.CloudRegionID = cr.ID
	m.model.CloudRegion = cr

	return nil
}

func (m *modelImporter) save(ctx context.Context) error {
	return m.jimm.Database.Transaction(func(d *db.Database) error {
		err := m.jimm.Database.AddModel(ctx, &m.model)
		if err != nil {
			if errors.ErrorCode(err) == errors.CodeAlreadyExists {
				return fmt.Errorf("model (%s) already exists", m.model.Name)
			}
			return err
		}
		for _, offer := range m.offersToAdd {
			dbOffer := dbmodel.ApplicationOffer{
				UUID:    offer.OfferUUID,
				Name:    offer.OfferName,
				URL:     offer.OfferURL,
				ModelID: m.model.ID,
			}
			if err := m.jimm.Database.AddApplicationOffer(ctx, &dbOffer); err != nil {
				if errors.ErrorCode(err) == errors.CodeAlreadyExists {
					return fmt.Errorf("offer with URL %s already exists", offer.OfferURL)
				}
				return err
			}
		}
		return nil
	})
}

// ImportModel imports a model and existing offers into JIMM.  A new owner  must be set to
// represent the external user who will own this model (if the original owner is a local user).
func (j *JIMM) ImportModel(ctx context.Context, user *openfga.User, controllerName string, modelTag names.ModelTag, newOwner string) error {
	const op = errors.Op("jimm.ImportModel")

	if err := j.checkJimmAdmin(user); err != nil {
		return err
	}

	importer, err := newModelImporter(j, newOwner)
	if err != nil {
		return errors.E(op, err)
	}

	if err := importer.fetchModelInfo(ctx, controllerName, modelTag); err != nil {
		return errors.E(op, err)
	}

	if err := importer.setModelOwner(ctx); err != nil {
		return errors.E(op, err)
	}

	if err := importer.addPermissions(ctx); err != nil {
		return errors.E(op, err)
	}

	// TODO(CSS-5458): Remove the below section on cloud credentials once we no longer persist the relation between
	// cloud credentials and models.
	// Update: We need to investigate this further, if a user updates their cloud-credential it will update the credential
	// on this model.
	if err := importer.setCloudCredential(ctx); err != nil {
		return errors.E(op, err)
	}

	if err := importer.setModelCloud(ctx); err != nil {
		return errors.E(op, err)
	}

	if err := importer.save(ctx); err != nil {
		return errors.E(op, err)
	}

	return nil
}

// UpdateMigratedModel asserts that the model has been migrated to the
// specified controller and updates the internal model representation.
func (j *JIMM) UpdateMigratedModel(ctx context.Context, user *openfga.User, modelTag names.ModelTag, targetControllerName string) error {
	const op = errors.Op("jimm.UpdateMigratedModel")

	if !user.JimmAdmin {
		return errors.E(op, errors.CodeUnauthorized, "unauthorized")
	}

	model := dbmodel.Model{
		UUID: sql.NullString{
			String: modelTag.Id(),
			Valid:  true,
		},
	}
	err := j.Database.GetModel(ctx, &model)
	if err != nil {
		if errors.ErrorCode(err) == errors.CodeNotFound {
			return errors.E(op, "model not found", errors.CodeModelNotFound)
		}
		return errors.E(op, err)
	}

	targetController := dbmodel.Controller{
		Name: targetControllerName,
	}
	err = j.Database.GetController(ctx, &targetController)
	if err != nil {
		if errors.ErrorCode(err) == errors.CodeNotFound {
			return errors.E(op, "controller not found", errors.CodeNotFound)
		}
		return errors.E(op, err)
	}

	// check the model is known to the controller
	api, err := j.dial(ctx, &targetController, names.ModelTag{})
	if err != nil {
		return errors.E(op, err)
	}
	defer api.Close()

	err = api.ModelInfo(ctx, &jujuparams.ModelInfo{
		UUID: modelTag.Id(),
	})
	if err != nil {
		return errors.E(op, err)
	}

	model.Controller = targetController
	model.ControllerID = targetController.ID
	err = j.Database.UpdateModel(ctx, &model)
	if err != nil {
		zapctx.Error(ctx, "failed to update model", zap.String("model", model.UUID.String), zaputil.Error(err))
		return errors.E(op, err)
	}

	return nil
}

// InitiateMigration triggers the migration of the specified model to a target controller.
// externalMigration indicates whether this model is moving to a controller managed by
// JIMM or not.
func (j *JIMM) InitiateMigration(ctx context.Context, user *openfga.User, spec jujuparams.MigrationSpec) (jujuparams.InitiateMigrationResult, error) {
	const op = errors.Op("jimm.InitiateMigration")

	result := jujuparams.InitiateMigrationResult{
		ModelTag: spec.ModelTag,
	}
	mt, err := names.ParseModelTag(spec.ModelTag)
	if err != nil {
		return result, errors.E(op, err, errors.CodeBadRequest)
	}
	isAdministrator, err := openfga.IsAdministrator(ctx, user, mt)
	if err != nil {
		return result, errors.E(op, err, errors.CodeOpenFGARequestFailed)
	}
	if !isAdministrator {
		return result, errors.E(op, errors.CodeUnauthorized)
	}

	targetControllerTag, err := names.ParseControllerTag(spec.TargetInfo.ControllerTag)
	if err != nil {
		return result, errors.E(op, err, errors.CodeBadRequest)
	}

	targetUserTag, err := names.ParseUserTag(spec.TargetInfo.AuthTag)
	if err != nil {
		return result, errors.E(op, err, errors.CodeBadRequest)
	}

	var targetMacaroons []macaroon.Slice
	if spec.TargetInfo.Macaroons != "" {
		err = json.Unmarshal([]byte(spec.TargetInfo.Macaroons), &targetMacaroons)
		if err != nil {
			return result, errors.E(op, err, "failed to unmarshal macaroons", errors.CodeBadRequest)
		}
	}

	model := dbmodel.Model{}
	model.SetTag(mt)
	err = j.Database.GetModel(ctx, &model)
	if err != nil {
		return result, errors.E(op, "failed to retrieve the model from the database", err)
	}

	api, err := j.dial(ctx, &model.Controller, names.ModelTag{})
	if err != nil {
		return result, errors.E(op, "failed to dial the controller", err)
	}

	client := newControllerClient(api)
	defer client.Close()

	result.MigrationId, err = client.InitiateMigration(controller.MigrationSpec{
		ModelUUID:             mt.Id(),
		TargetControllerUUID:  targetControllerTag.Id(),
		TargetControllerAlias: spec.TargetInfo.ControllerAlias,
		TargetAddrs:           spec.TargetInfo.Addrs,
		TargetCACert:          spec.TargetInfo.CACert,
		TargetUser:            targetUserTag.Id(),
		TargetPassword:        spec.TargetInfo.Password,
		TargetMacaroons:       targetMacaroons,
	})
	if err != nil {
		return result, errors.E(op, err)
	}
	return result, nil
}
