// Copyright 2024 Canonical.

package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/juju/cmd/v3"
	jujucmdv3 "github.com/juju/cmd/v3"
	"github.com/juju/gnuflag"
	jujuapi "github.com/juju/juju/api"
	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/jujuclient"

	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/pkg/api"
	apiparams "github.com/canonical/jimm/v3/pkg/api/params"
)

const (
	groupDoc = `
The group command enables group management for jimm
`

	addGroupDoc = `
The add command adds group to jimm.
`
	addGroupExample = `
    jimmctl auth group add mygroup
`
	renameGroupDoc = `
The rename command renames a group in jimm.
`
	renameGroupExample = `
    jimmctl auth group rename mygroup newgroup
`
	removeGroupDoc = `
The remove command removes a group in jimm.
`

	removeGroupExample = `
    jimmctl auth group remove mygroup
`

	listGroupsDoc = `
The list command lists all groups in jimm.
`
	listGroupsExample = `
    jimmctl auth group list
`
)

// NewGroupCommand returns a command for group management.
func NewGroupCommand() *jujucmdv3.SuperCommand {
	cmd := jujucmd.NewSuperCommand(jujucmdv3.SuperCommandParams{
		Name:        "group",
		UsagePrefix: "auth",
		Doc:         groupDoc,
		Purpose:     "Group management.",
	})
	cmd.Register(newAddGroupCommand())
	cmd.Register(newRenameGroupCommand())
	cmd.Register(newRemoveGroupCommand())
	cmd.Register(newListGroupsCommand())

	return cmd
}

// newAddGroupCommand returns a command to add a group.
func newAddGroupCommand() cmd.Command {
	cmd := &addGroupCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// addGroupCommand adds a group.
type addGroupCommand struct {
	modelcmd.ControllerCommandBase
	out cmd.Output

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	name string
}

// Info implements the cmd.Command interface.
func (c *addGroupCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "add",
		Args:     "<name>",
		Purpose:  "Add group to jimm.",
		Doc:      addGroupDoc,
		Examples: addGroupExample,
	})
}

// SetFlags implements Command.SetFlags.
func (c *addGroupCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	c.out.AddFlags(f, "yaml", map[string]cmd.Formatter{
		"yaml": cmd.FormatYaml,
		"json": cmd.FormatJson,
	})
}

// Init implements the cmd.Command interface.
func (c *addGroupCommand) Init(args []string) error {
	if len(args) < 1 {
		return errors.E("group name not specified")
	}
	c.name, args = args[0], args[1:]
	if len(args) > 0 {
		return errors.E("too many args")
	}
	return nil
}

// Run implements Command.Run.
func (c *addGroupCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	client := api.NewClient(apiCaller)
	resp, err := client.AddGroup(&apiparams.AddGroupRequest{
		Name: c.name,
	})
	if err != nil {
		return errors.E(err)
	}

	err = c.out.Write(ctxt, resp)
	if err != nil {
		return errors.E(err)
	}
	return nil
}

// newRenameGroupCommand returns a command to rename a group.
func newRenameGroupCommand() cmd.Command {
	cmd := &renameGroupCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// renameGroupCommand renames a group.
type renameGroupCommand struct {
	modelcmd.ControllerCommandBase

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	name    string
	newName string
}

// Info implements the cmd.Command interface.
func (c *renameGroupCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "rename",
		Args:     "<name> <new name>",
		Purpose:  "Rename a group.",
		Doc:      renameGroupDoc,
		Examples: renameGroupExample,
	})
}

// Init implements the cmd.Command interface.
func (c *renameGroupCommand) Init(args []string) error {
	if len(args) < 2 {
		return errors.E("group name not specified")
	}
	c.name, c.newName, args = args[0], args[1], args[2:]
	if len(args) > 0 {
		return errors.E("too many args")
	}
	return nil
}

// Run implements Command.Run.
func (c *renameGroupCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	params := apiparams.RenameGroupRequest{
		Name:    c.name,
		NewName: c.newName,
	}

	client := api.NewClient(apiCaller)
	err = client.RenameGroup(&params)
	if err != nil {
		return errors.E(err)
	}

	return nil
}

// newRemoveGroupCommand returns a command to Remove a group.
func newRemoveGroupCommand() cmd.Command {
	cmd := &removeGroupCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// removeGroupCommand Removes a group.
type removeGroupCommand struct {
	modelcmd.ControllerCommandBase
	out cmd.Output

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	name  string
	force bool
}

// Info implements the cmd.Command interface.
func (c *removeGroupCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "remove",
		Args:     "<name>",
		Purpose:  "Remove a group.",
		Doc:      removeGroupDoc,
		Examples: removeGroupExample,
	})
}

// Init implements the cmd.Command interface.
func (c *removeGroupCommand) Init(args []string) error {
	if len(args) < 1 {
		return errors.E("group name not specified")
	}
	c.name, args = args[0], args[1:]
	if len(args) > 0 {
		return errors.E("too many args")
	}
	return nil
}

// SetFlags implements Command.SetFlags.
func (c *removeGroupCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	c.out.AddFlags(f, "smart", map[string]cmd.Formatter{
		"smart": cmd.FormatSmart,
	})
	f.BoolVar(&c.force, "y", false, "delete group without prompt")
}

// Run implements Command.Run.
func (c *removeGroupCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	if !c.force {
		reader := bufio.NewReader(ctxt.Stdin)
		// Using Fprintf over c.out.write to avoid printing a new line.
		_, err := fmt.Fprintf(ctxt.Stdout, "This will also delele all associated relations.\nConfirm you would like to delete group %q (y/N): ", c.name)
		if err != nil {
			return err
		}
		text, err := reader.ReadString('\n')
		if err != nil {
			return errors.E(err, "Failed to read from input.")
		}
		text = strings.ReplaceAll(text, "\n", "")
		if !(text == "y" || text == "Y") {
			return nil
		}
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	params := apiparams.RemoveGroupRequest{
		Name: c.name,
	}

	client := api.NewClient(apiCaller)
	err = client.RemoveGroup(&params)
	if err != nil {
		return errors.E(err)
	}

	return nil
}

// newListGroupsCommand returns a command to list all groups.
func newListGroupsCommand() cmd.Command {
	cmd := &listGroupsCommand{
		store: jujuclient.NewFileClientStore(),
	}

	return modelcmd.WrapBase(cmd)
}

// listGroupsCommand Lists all groups.
type listGroupsCommand struct {
	modelcmd.ControllerCommandBase
	out cmd.Output

	store    jujuclient.ClientStore
	dialOpts *jujuapi.DialOpts

	limit  int
	offset int
}

// Info implements the cmd.Command interface.
func (c *listGroupsCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:     "list",
		Purpose:  "List all groups.",
		Doc:      listGroupsDoc,
		Examples: listGroupsExample,
	})
}

// Init implements the cmd.Command interface.
func (c *listGroupsCommand) Init(args []string) error {
	if len(args) > 0 {
		return errors.E("too many args")
	}
	return nil
}

// SetFlags implements Command.SetFlags.
func (c *listGroupsCommand) SetFlags(f *gnuflag.FlagSet) {
	c.CommandBase.SetFlags(f)
	c.out.AddFlags(f, "yaml", map[string]cmd.Formatter{
		"yaml": cmd.FormatYaml,
		"json": cmd.FormatJson,
	})
	f.IntVar(&c.limit, "limit", 0, "The maximum number of groups to return")
	f.IntVar(&c.offset, "offset", 0, "The offset to use when requesting groups")
}

// Run implements Command.Run.
func (c *listGroupsCommand) Run(ctxt *cmd.Context) error {
	currentController, err := c.store.CurrentController()
	if err != nil {
		return errors.E(err, "could not determine controller")
	}

	apiCaller, err := c.NewAPIRootWithDialOpts(c.store, currentController, "", c.dialOpts)
	if err != nil {
		return err
	}

	client := api.NewClient(apiCaller)
	req := apiparams.ListGroupsRequest{Limit: c.limit, Offset: c.offset}
	groups, err := client.ListGroups(&req)
	if err != nil {
		return errors.E(err)
	}

	err = c.out.Write(ctxt, groups)
	if err != nil {
		return errors.E(err)
	}

	return nil
}
