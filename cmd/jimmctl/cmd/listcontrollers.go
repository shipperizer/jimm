// Copyright 2024 Canonical.

package cmd

import (
	"github.com/juju/cmd/v3"
	"github.com/juju/gnuflag"
	jujuapi "github.com/juju/juju/api"
	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/jujuclient"

	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/pkg/api"
)

const (
	listControllersCommandDoc = `
The list-controllers command displays controller information
for all controllers known to JIMM.
`
	listControllersCommandExample = `
    jimmctl controllers 
    jimmctl controllers --format json
`
)

// NewListControllersCommand returns a command to list controller information.
func NewListControllersCommand() cmd.Command {
	cmd := &listControllersCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// listControllersCommand shows controller information
// for all controllers known to JIMM.
type listControllersCommand struct {
	modelcmd.ControllerCommandBase
	out cmd.Output

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts
}

func (c *listControllersCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "controllers",
		Purpose:  "Lists all controllers known to JIMM.",
		Doc:      listControllersCommandDoc,
		Examples: listControllersCommandExample,
		Aliases:  []string{"list-controllers"},
	})
}

// SetFlags implements Command.SetFlags.
func (c *listControllersCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	c.out.AddFlags(f, "yaml", map[string]cmd.Formatter{
		"yaml": cmd.FormatYaml,
		"json": cmd.FormatJson,
	})
}

// Run implements Command.Run.
func (c *listControllersCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}
	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	client := api.NewClient(apiCaller)
	controllers, err := client.ListControllers()
	if err != nil {
		return errors.E(err)
	}

	err = c.out.Write(ctxt, controllers)
	if err != nil {
		return errors.E(err)
	}
	return nil
}
