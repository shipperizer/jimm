// Copyright 2016 Canonical Ltd.

package jujuapi

import (
	"context"
	"fmt"

	jujuparams "github.com/juju/juju/apiserver/params"
	"github.com/juju/names/v4"
	"github.com/rogpeppe/fastuuid"

	"github.com/CanonicalLtd/jimm/internal/dbmodel"
	"github.com/CanonicalLtd/jimm/internal/errors"
	"github.com/CanonicalLtd/jimm/internal/jujuapi/rpc"
	jimmversion "github.com/CanonicalLtd/jimm/version"
)

func init() {
	facadeInit["Controller"] = func(r *controllerRoot) []int {
		allModelsMethod := rpc.Method(r.AllModels)
		configSetMethod := rpc.Method(r.ConfigSet)
		controllerConfigMethod := rpc.Method(r.ControllerConfig)
		controllerVersionMethod := rpc.Method(r.ControllerVersion)
		identityProviderURLMethod := rpc.Method(r.IdentityProviderURL)
		modelConfigMethod := rpc.Method(r.ModelConfig)
		modelStatusMethod := rpc.Method(r.ModelStatus)
		mongoVersionMethod := rpc.Method(r.MongoVersion)
		watchModelSummariesMethod := rpc.Method(r.WatchModelSummaries)

		r.AddMethod("Controller", 3, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 3, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 3, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 3, "ModelStatus", modelStatusMethod)

		r.AddMethod("Controller", 4, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 4, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 4, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 4, "ModelStatus", modelStatusMethod)

		r.AddMethod("Controller", 5, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 5, "ConfigSet", configSetMethod)
		r.AddMethod("Controller", 5, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 5, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 5, "ModelStatus", modelStatusMethod)

		r.AddMethod("Controller", 6, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 6, "ConfigSet", configSetMethod)
		r.AddMethod("Controller", 6, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 6, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 6, "ModelStatus", modelStatusMethod)
		r.AddMethod("Controller", 6, "MongoVersion", mongoVersionMethod)

		r.AddMethod("Controller", 7, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 7, "ConfigSet", configSetMethod)
		r.AddMethod("Controller", 7, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 7, "IdentityProviderURL", identityProviderURLMethod)
		r.AddMethod("Controller", 7, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 7, "ModelStatus", modelStatusMethod)
		r.AddMethod("Controller", 7, "MongoVersion", mongoVersionMethod)

		r.AddMethod("Controller", 8, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 8, "ConfigSet", configSetMethod)
		r.AddMethod("Controller", 8, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 8, "ControllerVersion", controllerVersionMethod)
		r.AddMethod("Controller", 8, "IdentityProviderURL", identityProviderURLMethod)
		r.AddMethod("Controller", 8, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 8, "ModelStatus", modelStatusMethod)
		r.AddMethod("Controller", 8, "MongoVersion", mongoVersionMethod)

		r.AddMethod("Controller", 9, "AllModels", allModelsMethod)
		r.AddMethod("Controller", 9, "ConfigSet", configSetMethod)
		r.AddMethod("Controller", 9, "ControllerConfig", controllerConfigMethod)
		r.AddMethod("Controller", 9, "ControllerVersion", controllerVersionMethod)
		r.AddMethod("Controller", 9, "IdentityProviderURL", identityProviderURLMethod)
		r.AddMethod("Controller", 9, "ModelConfig", modelConfigMethod)
		r.AddMethod("Controller", 9, "ModelStatus", modelStatusMethod)
		r.AddMethod("Controller", 9, "MongoVersion", mongoVersionMethod)
		r.AddMethod("Controller", 9, "WatchModelSummaries", watchModelSummariesMethod)

		return []int{3, 4, 5, 6, 7, 8, 9}
	}
}

// ConfigSet changes the value of specified controller configuration
// settings. Only some settings can be changed after bootstrap.
// JIMM does not support changing settings via ConfigSet.
func (r *controllerRoot) ConfigSet(ctx context.Context, args jujuparams.ControllerConfigSet) error {
	return nil
}

// MongoVersion allows the introspection of the mongo version per controller.
func (r *controllerRoot) MongoVersion(ctx context.Context) (jujuparams.StringResult, error) {
	return jujuparams.StringResult{}, errors.E(errors.CodeNotImplemented)
}

// IdentityProviderURL returns the URL of the configured external identity
// provider for this controller or an empty string if no external identity
// provider has been configured when the controller was bootstrapped.
func (r *controllerRoot) IdentityProviderURL(ctx context.Context) (jujuparams.StringResult, error) {
	return jujuparams.StringResult{
		Result: r.params.IdentityLocation,
	}, nil
}

// ControllerVersion returns the version information associated with this
// controller binary.
func (r *controllerRoot) ControllerVersion(ctx context.Context) (jujuparams.ControllerVersionResults, error) {
	const op = errors.Op("jujuapi.ControllerVersion")

	srvVersion, err := r.jimm.EarliestControllerVersion(ctx)
	if err != nil {
		return jujuparams.ControllerVersionResults{}, errors.E(op, err)
	}
	result := jujuparams.ControllerVersionResults{
		Version:   srvVersion.String(),
		GitCommit: jimmversion.VersionInfo.GitCommit,
	}
	return result, nil
}

func (r *controllerRoot) WatchModelSummaries(ctx context.Context) (jujuparams.SummaryWatcherID, error) {
	const op = errors.Op("jujuapi.WatchModelSummaries")

	// TODO(mhilton) move this somewhere where it will be reused accross connections
	r.mu.Lock()
	if r.generator == nil {
		var err error
		r.generator, err = fastuuid.NewGenerator()
		r.mu.Unlock()
		if err != nil {
			return jujuparams.SummaryWatcherID{}, errors.E(op, err)
		}
	} else {
		r.mu.Unlock()
	}

	id := fmt.Sprintf("%v", r.generator.Next())

	watcher, err := newModelSummaryWatcher(ctx, id, r, r.jimm.Pubsub)
	if err != nil {
		return jujuparams.SummaryWatcherID{}, errors.E(op, err)
	}
	r.watchers.register(watcher)

	return jujuparams.SummaryWatcherID{
		WatcherID: id,
	}, nil
}

func (r *controllerRoot) AllModels(ctx context.Context) (jujuparams.UserModelList, error) {
	return r.allModels(ctx)
}

// allModels returns all the models the logged in user has access to.
func (r *controllerRoot) allModels(ctx context.Context) (jujuparams.UserModelList, error) {
	const op = errors.Op("jujuapi.AllModels")

	var models []jujuparams.UserModel
	err := r.jimm.ForEachUserModel(ctx, r.user, func(uma *dbmodel.UserModelAccess) error {
		models = append(models, uma.ToJujuUserModel())
		return nil
	})
	if err != nil {
		return jujuparams.UserModelList{}, errors.E(op, err)
	}
	return jujuparams.UserModelList{
		UserModels: models,
	}, nil
}

func (r *controllerRoot) ModelStatus(ctx context.Context, args jujuparams.Entities) (jujuparams.ModelStatusResults, error) {
	const op = errors.Op("jujuapi.ModelStatus")

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	results := make([]jujuparams.ModelStatus, len(args.Entities))
	for i, arg := range args.Entities {
		mt, err := names.ParseModelTag(arg.Tag)
		if err != nil {
			results[i].Error = mapError(errors.E(op, err, errors.CodeBadRequest))
			continue
		}
		status, err := r.jimm.ModelStatus(ctx, r.user, mt)
		if err != nil {
			results[i].Error = mapError(errors.E(op, err))
			continue
		}
		results[i] = *status
	}
	return jujuparams.ModelStatusResults{
		Results: results,
	}, nil
}

// ControllerConfig returns the controller's configuration.
func (r *controllerRoot) ControllerConfig() (jujuparams.ControllerConfigResult, error) {
	result := jujuparams.ControllerConfigResult{
		Config: map[string]interface{}{
			"charmstore-url": r.params.CharmstoreLocation,
			"metering-url":   r.params.MeteringLocation,
		},
	}
	return result, nil
}

// ModelConfig returns implements the controller facade's ModelConfig
// method. This always returns a permission error, as no user has admin
// access to the controller.
func (r *controllerRoot) ModelConfig() (jujuparams.ModelConfigResults, error) {
	return jujuparams.ModelConfigResults{}, errors.E(errors.CodeUnauthorized, "permission denied")
}
