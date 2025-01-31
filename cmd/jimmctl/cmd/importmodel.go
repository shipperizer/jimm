// Copyright 2024 Canonical.

package cmd

import (
	"github.com/juju/cmd/v3"
	"github.com/juju/gnuflag"
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
	importModelCommandDoc = `
The import-model imports a model running on a controller to jimm.

When importing, it is necessary for JIMM to contain a set of cloud credentials
that represent a user's access to the incoming model's cloud. 

The --owner command is necessary when importing a model created by a 
local user and it will switch the model owner to the desired external user.
`
	importModelCommandExample = `
    jimmctl import-model mycontroller ac30d6ae-0bed-4398-bba7-75d49e39f189
    jimmctl import-model mycontroller ac30d6ae-0bed-4398-bba7-75d49e39f189 --owner user@canonical.com
`
)

// NewImportModelCommand returns a command to import a model.
func NewImportModelCommand() cmd.Command {
	cmd := &importModelCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// importModelCommand imports a model.
type importModelCommand struct {
	modelcmd.ControllerCommandBase
	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	req apiparams.ImportModelRequest
}

// Info implements the cmd.Command interface.
func (c *importModelCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "import-model",
		Args:     "<controller name> <model uuid>",
		Purpose:  "Import a model to jimm",
		Doc:      importModelCommandDoc,
		Examples: importModelCommandExample,
	})
}

// SetFlags implements Command.SetFlags.
func (c *importModelCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	f.StringVar(&c.req.Owner, "owner", "", "switch the model owner to the desired user")
}

// Init implements the cmd.Command interface.
func (c *importModelCommand) Init(args []string) error {
	switch len(args) {
	default:
		return errors.E("too many args")
	case 0:
		return errors.E("controller not specified")
	case 1:
		return errors.E("model uuid not specified")
	case 2:
	}

	c.req.Controller = args[0]
	if !names.IsValidModel(args[1]) {
		return errors.E("invalid model uuid")
	}
	c.req.ModelTag = names.NewModelTag(args[1]).String()
	return nil
}

// Run implements Command.Run.
func (c *importModelCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	client := api.NewClient(apiCaller)
	if err := client.ImportModel(&c.req); err != nil {
		return errors.E(err)
	}
	return nil
}
