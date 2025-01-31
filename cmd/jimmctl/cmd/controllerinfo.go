// Copyright 2024 Canonical.

package cmd

import (
	"os"

	"github.com/juju/cmd/v3"
	"github.com/juju/errors"
	"github.com/juju/gnuflag"
	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/jujuclient"
	"sigs.k8s.io/yaml"

	apiparams "github.com/canonical/jimm/v3/pkg/api/params"
)

const (
	controllerInfoCommandDoc = `
The controller-info command writes controller information contained
in the juju client store to a yaml file.

If a public address is specified, the output controller information
will contain the public address provided and omit a CA cert, this assumes
that the server is secured with a public certificate.

Use the --local flag if the server is not configured with a public address.
`
	controllerInfoCommandExample = `
    jimmctl controller-info mycontroller ./destination/file.yaml mycontroller.example.com 
    jimmctl controller-info mycontroller ./destination/file.yaml --local
`
)

// NewControllerInfoCommand returns a command that writes
// controller information to a yaml file.
func NewControllerInfoCommand() cmd.Command {
	cmd := &controllerInfoCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// controllerInfoCommand writes controller information
// to a yaml file.
type controllerInfoCommand struct {
	modelcmd.ControllerCommandBase

	store          jujuclient.ClientStore
	controllerName string
	publicAddress  string
	file           cmd.FileVar
	local          bool
	tlsHostname    string
}

func (c *controllerInfoCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "controller-info",
		Args:     "<name> <filepath> [<public address>]",
		Purpose:  "Stores controller info to a yaml file",
		Doc:      controllerInfoCommandDoc,
		Examples: controllerInfoCommandExample,
	})
}

// SetFlags implements Command.SetFlags.
func (c *controllerInfoCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	f.BoolVar(&c.local, "local", false, "If local flag is specified, then the local API address and CA cert of the controller will be used.")
	f.StringVar(&c.tlsHostname, "tls-hostname", "", "Specify the hostname for TLS verification.")
}

// Init implements the cmd.Command interface.
func (c *controllerInfoCommand) Init(args []string) error {
	if len(args) < 2 {
		return errors.New("controller name or filename not specified")
	}
	c.controllerName, c.file.Path = args[0], args[1]
	if len(args) == 3 {
		c.publicAddress = args[2]
	}
	if len(args) > 3 {
		return errors.New("too many args")
	}
	if c.local && len(c.publicAddress) > 0 {
		return errors.New("cannot set both public address and local flag")
	}
	if !c.local && len(c.publicAddress) == 0 {
		return errors.New("provide either a public address or use --local")
	}
	return nil
}

// Run implements Command.Run.
func (c *controllerInfoCommand) Run(ctxt *cmd.Context) error {
	controller, err := c.store.ControllerByName(c.controllerName)
	if err != nil {
		return errors.Mask(err)
	}

	accountDetails, err := c.store.AccountDetails(c.controllerName)
	if err != nil {
		return errors.Mask(err)
	}

	info := apiparams.AddControllerRequest{
		UUID:         controller.ControllerUUID,
		Name:         c.controllerName,
		APIAddresses: controller.APIEndpoints,
		Username:     accountDetails.User,
		Password:     accountDetails.Password,
	}

	info.TLSHostname = c.tlsHostname
	info.PublicAddress = c.publicAddress
	if c.local {
		info.CACertificate = controller.CACert
	}
	data, err := yaml.Marshal(info)
	if err != nil {
		return errors.Mask(err)
	}
	err = os.WriteFile(c.file.Path, data, 0600)
	if err != nil {
		return errors.Mask(err)
	}
	return nil
}
