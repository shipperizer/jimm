// Copyright 2024 Canonical.

package cmd

import (
	"fmt"

	"github.com/juju/cmd/v3"
	"github.com/juju/gnuflag"
	jujuapi "github.com/juju/juju/api"
	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/jujuclient"
	"sigs.k8s.io/yaml"

	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/pkg/api"
	apiparams "github.com/canonical/jimm/v3/pkg/api/params"
)

var (
	// stdinMarkers contains file names that are taken to be stdin.
	stdinMarkers = []string{"-"}

	addControllerCommandDoc = `
The add-controller command adds a controller to jimm.
`
	addControllerCommandExample = `
    jimmctl add-controller ./controller-info 
    jimmctl add-controller ./controller-info.yaml --format json
`
)

// NewAddControllerCommand returns a command to add a controller.
func NewAddControllerCommand() cmd.Command {
	cmd := &addControllerCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// addControllerCommand adds a controller.
type addControllerCommand struct {
	modelcmd.ControllerCommandBase
	out cmd.Output

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts
	file     cmd.FileVar
}

func (c *addControllerCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "add-controller",
		Purpose:  "Add controller to jimm",
		Args:     "<filepath>",
		Doc:      addControllerCommandDoc,
		Examples: addControllerCommandExample,
	})
}

// SetFlags implements Command.SetFlags.
func (c *addControllerCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	c.out.AddFlags(f, "yaml", map[string]cmd.Formatter{
		"yaml": cmd.FormatYaml,
		"json": cmd.FormatJson,
	})
	c.file.StdinMarkers = stdinMarkers
}

// Init implements the cmd.Command interface.
func (c *addControllerCommand) Init(args []string) error {
	if len(args) < 1 {
		return errors.E("filename not specified")
	}
	c.file.Path = args[0]
	if len(args) > 1 {
		return errors.E("too many args")
	}
	return nil
}

// Run implements Command.Run.
func (c *addControllerCommand) Run(ctxt *cmd.Context) error {
	fmt.Println("c.store.CurrentController()")
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	fmt.Println("c.NewAPIRootWithDialOpts")
	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	fmt.Println("unmarshalYAMLFile")
	var params apiparams.AddControllerRequest
	if err = unmarshalYAMLFile(ctxt, &params, c.file); err != nil {
		return errors.E(err)
	}
	fmt.Println("api.NewClient(apiCaller)")
	client := api.NewClient(apiCaller)
	fmt.Println("client.AddController(&params)")
	info, err := client.AddController(&params)
	if err != nil {
		return errors.E(err)
	}
	fmt.Println("c.out.Write(ctxt, info)")
	err = c.out.Write(ctxt, info)
	if err != nil {
		return errors.E(err)
	}
	return nil
}

func unmarshalYAMLFile(ctxt *cmd.Context, v interface{}, fv cmd.FileVar) error {
	buf, err := fv.Read(ctxt)
	if err != nil {
		return errors.E(err)
	}

	err = yaml.Unmarshal(buf, &v)
	if err != nil {
		return errors.E(err)
	}
	return nil
}
