// Copyright 2024 Canonical.

package openfga

import (
	"context"
	"strings"

	cofga "github.com/canonical/ofga"
	"github.com/juju/names/v5"

	"github.com/canonical/jimm/v3/internal/errors"
	ofganames "github.com/canonical/jimm/v3/internal/openfga/names"
	"github.com/canonical/jimm/v3/internal/servermon"
	jimmnames "github.com/canonical/jimm/v3/pkg/names"
)

var (
	// resourceTypes contains a list of all resource kinds (i.e. tags) used throughout JIMM.
	resourceTypes = [...]string{names.UserTagKind, names.ModelTagKind, names.ControllerTagKind, names.ApplicationOfferTagKind, jimmnames.GroupTagKind, jimmnames.ServiceAccountTagKind, jimmnames.RoleTagKind}
)

// Tuple represents a relation between an object and a target.
type Tuple = cofga.Tuple

// Tag represents an entity tag as used by JIMM in OpenFGA.
type Tag = cofga.Entity

// ParseTag parses an entity tag from string representation.
var ParseTag = cofga.ParseEntity

// Kind represents the type of a tag kind.
type Kind = cofga.Kind

// Relation holds the type of tag relation.
type Relation = cofga.Relation

// Object Kinds, these are OpenFGA object Kinds that reference
// Juju/JIMM objects. These are included here for ease of use
// and avoiding string constants.
var (
	// UserType represents a user object.
	UserType Kind = names.UserTagKind
	// ModelType represents a model object.
	ModelType Kind = names.ModelTagKind
	// GroupType represents a group object.
	GroupType Kind = jimmnames.GroupTagKind
	// RoleType represents a role object.
	RoleType Kind = jimmnames.RoleTagKind
	// ApplicationOfferType represents an application offer object.
	ApplicationOfferType Kind = names.ApplicationOfferTagKind
	// CloudType represents a cloud object.
	CloudType Kind = names.CloudTagKind
	// ControllerType represents a controller object.
	ControllerType Kind = names.ControllerTagKind
	// ServiceAccountType represents a service account.
	ServiceAccountType Kind = jimmnames.ServiceAccountTagKind
)

// OFGAClient contains convenient utility methods for interacting
// with OpenFGA from OUR usecase. It wraps the provided pre-generated client
// from OpenFGA.
//
// It makes no promises as to whether the underlying client is "correctly configured" however.
//
// It is worth noting that any time the term 'User' is used, this COULD represent ANOTHER object, for example:
// a group can relate to a user as a 'member', if a user is a 'member' of that group, and that group
// is an administrator of the controller, a byproduct of this is that the flow will look like so:
//
// user:alex -> member -> group:yellow -> administrator -> controller:<uuid>
//
// In the above scenario, alex becomes an administrator due the the 'user' aka group:yellow being
// an administrator.
type OFGAClient struct {
	cofgaClient *cofga.Client
}

// NewOpenFGAClient returns a new JIMM-specific client that wraps the given core OpenFGA client.
func NewOpenFGAClient(cofgaClient *cofga.Client) *OFGAClient {
	return &OFGAClient{cofgaClient: cofgaClient}
}

// publicAccessAdaptor handles cases where a tuple need to be transformed before being
// returned to the application layer. The wildcard tuple * for users is replaced
// with the everyone user.
func publicAccessAdaptor(tt cofga.TimestampedTuple) cofga.TimestampedTuple {
	if tt.Tuple.Object.Kind == UserType && tt.Tuple.Object.IsPublicAccess() {
		tt.Tuple.Object.ID = ofganames.EveryoneUser
	}
	return tt
}

// setResourceAccess creates a relation to model the requested resource access.
// Note that the action is idempotent (does not return error if the relation already exists).
func (o *OFGAClient) setResourceAccess(ctx context.Context, object, target names.Tag, relation Relation) error {
	if object == nil || target == nil {
		return errors.E("missing object or target for relation")
	}
	err := o.AddRelation(ctx, Tuple{
		Object:   ofganames.ConvertGenericTag(object),
		Relation: relation,
		Target:   ofganames.ConvertGenericTag(target),
	})
	if err != nil {
		// if the tuple already exist we don't return an error.
		// TODO we should opt to check against specific errors via checking their code/metadata.
		if strings.Contains(err.Error(), "cannot write a tuple which already exists") {
			return nil
		}
		return errors.E(err)
	}
	return nil
}

// unsetResourceAccess deletes a relation that corresponds to the requested resource access.
// Note that the action is idempotent (does not return error if the relation does not exist).
func (o *OFGAClient) unsetResourceAccess(ctx context.Context, object, target names.Tag, relation Relation) error {
	if object == nil || target == nil {
		return errors.E("missing object or target for relation")
	}
	err := o.RemoveRelation(ctx, Tuple{
		Object:   ofganames.ConvertGenericTag(object),
		Relation: relation,
		Target:   ofganames.ConvertGenericTag(target),
	})
	if err != nil {
		// if the tuple already exist we don't return an error.
		// TODO we should opt to check against specific errors via checking their code/metadata.
		if strings.Contains(err.Error(), "cannot delete a tuple which does not exist") {
			return nil
		}
		return errors.E(err)
	}
	return nil
}

// getRelatedObjects returns all objects where the user has a valid relation to them.
// Such as all the groups a user resides in.
//
// The results may be paginated via a pageSize and the initial returned continuation token from the first request.
func (o *OFGAClient) getRelatedObjects(ctx context.Context, tuple Tuple, pageSize int32, continuationToken string) ([]Tuple, string, error) {
	timestampedTuples, ct, err := o.cofgaClient.FindMatchingTuples(ctx, tuple, pageSize, continuationToken)
	if err != nil {
		return nil, "", err
	}
	tuples := make([]Tuple, len(timestampedTuples))
	for i, tt := range timestampedTuples {
		tt := publicAccessAdaptor(tt)
		tuples[i] = tt.Tuple
	}
	return tuples, ct, nil
}

// listObjects lists all the object IDs a user has access to via the specified relation
// It is experimental and subject to context deadlines. See: https://openfga.dev/docs/interacting/relationship-queries#summary
//
// The arguments for this call also slightly differ, as it is not an ordinary tuple to be set. The target object
// must NOT include the ID, i.e.,
//
//   - "group:" vs "group:mygroup", where "mygroup" is the ID and the correct objType would be "group".
func (o *OFGAClient) listObjects(ctx context.Context, user *Tag, relation Relation, objType Kind, contextualTuples []Tuple) (objectIds []Tag, err error) {
	entities, err := o.cofgaClient.FindAccessibleObjectsByRelation(ctx, Tuple{
		Object:   user,
		Relation: relation,
		Target:   &Tag{Kind: objType},
	}, contextualTuples...)
	if err != nil {
		return nil, err
	}
	return entities, nil
}

// AddRelation adds given relations (tuples).
func (o *OFGAClient) AddRelation(ctx context.Context, tuples ...Tuple) (err error) {
	op := errors.Op("openfga.AddRelation")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	return o.cofgaClient.AddRelation(ctx, tuples...)
}

// RemoveRelation removes given relations (tuples).
func (o *OFGAClient) RemoveRelation(ctx context.Context, tuples ...Tuple) (err error) {
	op := errors.Op("openfga.RemoveRelation")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	return o.cofgaClient.RemoveRelation(ctx, tuples...)
}

// ListObjects returns all object IDs of <objType> that a user has the relation <relation> to.
func (o *OFGAClient) ListObjects(ctx context.Context, user *Tag, relation Relation, objType Kind, contextualTuples []Tuple) (_ []Tag, err error) {
	op := errors.Op("openfga.ListObjects")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	return o.listObjects(ctx, user, relation, objType, contextualTuples)
}

// ReadRelations reads a relation(s) from the provided tuple where a match can be found.
//
// See: https://openfga.dev/api/service#/Relationship%20Tuples/Read
//
// You may read via pagination utilising the continuation token returned from the request.
func (o *OFGAClient) ReadRelatedObjects(ctx context.Context, tuple Tuple, pageSize int32, continuationToken string) (_ []Tuple, _ string, err error) {
	op := errors.Op("openfga.ReadRelatedObjects")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	return o.getRelatedObjects(ctx, tuple, pageSize, continuationToken)
}

// CheckRelation verifies that a user (or object) is allowed to access the target object by the specified relation.
//
// It will return a bool of simply true or false, denoting authorisation, and an error.
func (o *OFGAClient) CheckRelation(ctx context.Context, tuple Tuple, trace bool) (_ bool, err error) {
	op := errors.Op("openfga.CheckRelation")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	if trace {
		return o.cofgaClient.CheckRelationWithTracing(ctx, tuple)
	}
	return o.cofgaClient.CheckRelation(ctx, tuple)
}

// removeTuples iteratively reads through all the tuples with the parameters as supplied by tuple and deletes them.
func (o *OFGAClient) removeTuples(ctx context.Context, tuple Tuple) (err error) {
	op := errors.Op("openfga.removeTuples")

	durationObserver := servermon.DurationObserver(servermon.OpenFGACallDurationHistogram, string(op))
	defer durationObserver()
	defer servermon.ErrorCounter(servermon.OpenFGACallErrorCount, &err, string(op))

	// Note (babakks): an obvious improvement to this function is to make it work
	// atomically and remove all the tuples in a transaction. At the moment, it's
	// not simple, because OpenFGA supports limited number of write operation per
	// request (default is 100):
	// > "The number of write operations exceeds the allowed limit of 100"

	pageSize := 50
	for {
		// Since we're deleting the returned tuples, it's best to avoid pagination,
		// and fresh query for the relations.
		//nolint:gosec // The page size will not exceed int32.
		tuples, ct, err := o.ReadRelatedObjects(ctx, tuple, int32(pageSize), "")
		if err != nil {
			return err
		}
		if len(tuples) > 0 {
			err = o.RemoveRelation(ctx, tuples...)
			if err != nil {
				return err
			}
		}
		if ct == "" {
			return nil
		}
	}
}

// AddControllerModel adds a relation between a controller and a model.
func (o *OFGAClient) AddControllerModel(ctx context.Context, controller names.ControllerTag, model names.ModelTag) error {
	return o.setResourceAccess(ctx, controller, model, ofganames.ControllerRelation)
}

// RemoveControllerModel removes a relation between a controller and a model.
func (o *OFGAClient) RemoveControllerModel(ctx context.Context, controller names.ControllerTag, model names.ModelTag) error {
	return o.unsetResourceAccess(ctx, controller, model, ofganames.ControllerRelation)
}

// RemoveModel removes a model.
func (o *OFGAClient) RemoveModel(ctx context.Context, model names.ModelTag) error {
	if err := o.removeTuples(
		ctx,
		Tuple{
			Target: ofganames.ConvertTag(model),
		},
	); err != nil {
		return errors.E(err)
	}
	return nil
}

// AddModelApplicationOffer adds a relation between a model and an application offer.
func (o *OFGAClient) AddModelApplicationOffer(ctx context.Context, model names.ModelTag, offer names.ApplicationOfferTag) error {
	return o.setResourceAccess(ctx, model, offer, ofganames.ModelRelation)
}

// RemoveApplicationOffer removes an application offer.
func (o *OFGAClient) RemoveApplicationOffer(ctx context.Context, offer names.ApplicationOfferTag) error {
	if err := o.removeTuples(
		ctx,
		Tuple{
			Target: ofganames.ConvertTag(offer),
		},
	); err != nil {
		return errors.E(err)
	}
	return nil
}

// RemoveRole removes a role.
func (o *OFGAClient) RemoveRole(ctx context.Context, role jimmnames.RoleTag) error {
	// Remove all access to a group. I.e. user->group
	if err := o.removeTuples(
		ctx,
		Tuple{
			Relation: ofganames.AssigneeRelation,
			Target:   ofganames.ConvertTag(role),
		},
	); err != nil {
		return errors.E(err)
	}
	// Next remove all access that a group had. I.e. group->model
	// We need to loop through all resource types because the OpenFGA Read API does not provide
	// means for only specifying a user resource, it must be paired with an object type.
	for _, kind := range resourceTypes {
		kt, err := ofganames.BlankKindTag(kind)
		if err != nil {
			return errors.E(err)
		}
		newTuple := Tuple{
			Object: ofganames.ConvertTagWithRelation(role, ofganames.AssigneeRelation),
			Target: kt,
		}
		err = o.removeTuples(ctx, newTuple)
		if err != nil {
			return errors.E(err)
		}
	}
	return nil
}

// RemoveGroup removes a group.
func (o *OFGAClient) RemoveGroup(ctx context.Context, group jimmnames.GroupTag) error {
	// Remove all access to a group. I.e. user->group
	if err := o.removeTuples(
		ctx,
		Tuple{
			Relation: ofganames.MemberRelation,
			Target:   ofganames.ConvertTag(group),
		},
	); err != nil {
		return errors.E(err)
	}
	// Next remove all access that a group had. I.e. group->model
	// We need to loop through all resource types because the OpenFGA Read API does not provide
	// means for only specifying a user resource, it must be paired with an object type.
	for _, kind := range resourceTypes {
		kt, err := ofganames.BlankKindTag(kind)
		if err != nil {
			return errors.E(err)
		}
		newTuple := Tuple{
			Object: ofganames.ConvertTagWithRelation(group, ofganames.MemberRelation),
			Target: kt,
		}
		err = o.removeTuples(ctx, newTuple)
		if err != nil {
			return errors.E(err)
		}
	}
	return nil
}

// RemoveCloud removes a cloud.
func (o *OFGAClient) RemoveCloud(ctx context.Context, cloud names.CloudTag) error {
	if err := o.removeTuples(
		ctx,
		Tuple{
			Target: ofganames.ConvertTag(cloud),
		},
	); err != nil {
		return errors.E(err)
	}
	return nil
}

// AddCloudController adds a controller relation between a controller and
// a cloud.
func (o *OFGAClient) AddCloudController(ctx context.Context, cloud names.CloudTag, controller names.ControllerTag) error {
	return o.setResourceAccess(ctx, controller, cloud, ofganames.ControllerRelation)
}

// AddController adds a controller relation between JIMM and the added controller. Meaning
// JIMM admins also have administrator access to the added controller
func (o *OFGAClient) AddController(ctx context.Context, jimm names.ControllerTag, controller names.ControllerTag) error {
	return o.setResourceAccess(ctx, jimm, controller, ofganames.ControllerRelation)
}
