// Copyright 2024 Canonical.

package cmd

import (
	"github.com/juju/cmd/v3"
	jujuapi "github.com/juju/juju/api"
	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/jujuclient"
	"github.com/juju/names/v5"

	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/pkg/api"
	apiparams "github.com/canonical/jimm/v3/pkg/api/params"
)

const (
	updateMigratedModelCommandDoc = `
The update-migrated-model updates a model known to JIMM that has
been migrated externally to a different JAAS controller.
`
	updateMigratedModelCommandExample = `
    jimmctl update-migrated-model mycontroller e0bf3abf-7029-4e48-9c26-68a7b6e02947
`
)

// NewUpdateMigratedModelCommand returns a command to update the controller
// running a model.
func NewUpdateMigratedModelCommand() cmd.Command {
	cmd := &updateMigratedModelCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// updateMigratedModelCommand updates the controller running a model.
type updateMigratedModelCommand struct {
	modelcmd.ControllerCommandBase
	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	req apiparams.UpdateMigratedModelRequest
}

// Info implements the cmd.Command interface.
func (c *updateMigratedModelCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "update-migrated-model",
		Args:     "<controller name> <model uuid>",
		Purpose:  "Update the controller running a model.",
		Doc:      updateMigratedModelCommandDoc,
		Examples: updateMigratedModelCommandExample,
	})
}

// Init implements the cmd.Command interface.
func (c *updateMigratedModelCommand) Init(args []string) error {
	switch len(args) {
	default:
		return errors.E("too many args")
	case 0:
		return errors.E("controller not specified")
	case 1:
		return errors.E("model uuid not specified")
	case 2:
	}

	c.req.TargetController = args[0]
	if !names.IsValidModel(args[1]) {
		return errors.E("invalid model uuid")
	}
	c.req.ModelTag = names.NewModelTag(args[1]).String()
	return nil
}

// Run implements Command.Run.
func (c *updateMigratedModelCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	client := api.NewClient(apiCaller)
	if err := client.UpdateMigratedModel(&c.req); err != nil {
		return errors.E(err)
	}
	return nil
}
