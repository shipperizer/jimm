// Copyright 2024 Canonical.

package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"

	"github.com/canonical/jimm/v3/internal/db"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/errors"
)

func TestAddGroupUnconfiguredDatabase(t *testing.T) {
	c := qt.New(t)

	var d db.Database
	_, err := d.AddGroup(context.Background(), "test-group")
	c.Check(err, qt.ErrorMatches, `database not configured`)
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeServerConfiguration)
}

func (s *dbSuite) TestAddGroup(c *qt.C) {
	ctx := context.Background()

	uuid := uuid.NewString()
	c.Patch(db.NewUUID, func() string {
		return uuid
	})

	_, err := s.Database.AddGroup(ctx, "test-group")
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeUpgradeInProgress)

	err = s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	groupEntry, err := s.Database.AddGroup(ctx, "test-group")
	c.Assert(err, qt.IsNil)
	c.Assert(groupEntry.UUID, qt.Not(qt.Equals), "")

	_, err = s.Database.AddGroup(ctx, "test-group")
	c.Assert(errors.ErrorCode(err), qt.Equals, errors.CodeAlreadyExists)

	ge := dbmodel.GroupEntry{
		Name: "test-group",
	}
	tx := s.Database.DB.First(&ge)
	c.Assert(tx.Error, qt.IsNil)
	c.Assert(ge.ID, qt.Equals, uint(1))
	c.Assert(ge.Name, qt.Equals, "test-group")
	c.Assert(ge.UUID, qt.Equals, uuid)
}

func (s *dbSuite) TestCountGroups(c *qt.C) {
	err := s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	addNGroups := 10
	for i := range addNGroups {
		_, err := s.Database.AddGroup(context.Background(), fmt.Sprintf("test-group-%d", i))
		c.Assert(err, qt.IsNil)
	}
	count, err := s.Database.CountGroups(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(count, qt.Equals, addNGroups)
}

func (s *dbSuite) TestGetGroup(c *qt.C) {
	uuid1 := uuid.NewString()
	c.Patch(db.NewUUID, func() string {
		return uuid1
	})

	err := s.Database.GetGroup(context.Background(), &dbmodel.GroupEntry{
		Name: "test-group",
	})
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeUpgradeInProgress)

	err = s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	group := &dbmodel.GroupEntry{
		Name: "test-group",
	}
	err = s.Database.GetGroup(context.Background(), group)
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeNotFound)

	groupEntry, err := s.Database.AddGroup(context.TODO(), "test-group")
	c.Assert(err, qt.IsNil)
	c.Assert(groupEntry.UUID, qt.Equals, uuid1)

	err = s.Database.GetGroup(context.Background(), group)
	c.Check(err, qt.IsNil)
	c.Assert(group.ID, qt.Equals, uint(1))
	c.Assert(group.Name, qt.Equals, "test-group")
	c.Assert(group.UUID, qt.Equals, uuid1)

	uuid2 := uuid.NewString()
	c.Patch(db.NewUUID, func() string {
		return uuid2
	})

	groupEntry, err = s.Database.AddGroup(context.Background(), "test-group1")
	c.Assert(err, qt.IsNil)
	c.Assert(groupEntry.UUID, qt.Equals, uuid2)

	group = &dbmodel.GroupEntry{
		Name: "test-group1",
	}

	err = s.Database.GetGroup(context.Background(), group)
	c.Check(err, qt.IsNil)
	c.Assert(group.ID, qt.Equals, uint(2))
	c.Assert(group.Name, qt.Equals, "test-group1")
	c.Assert(group.UUID, qt.Equals, uuid2)
}

func (s *dbSuite) TestUpdateGroupName(c *qt.C) {
	err := s.Database.UpdateGroupName(context.Background(), "test-group", "new-name")
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeUpgradeInProgress)

	err = s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	err = s.Database.UpdateGroupName(context.Background(), "test-group", "new-name")
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeNotFound)

	_, err = s.Database.AddGroup(context.Background(), "test-group")
	c.Assert(err, qt.IsNil)

	ge1 := &dbmodel.GroupEntry{
		Name: "test-group",
	}
	err = s.Database.GetGroup(context.Background(), ge1)
	c.Assert(err, qt.IsNil)

	ge1.Name = "renamed-group"
	err = s.Database.UpdateGroupName(context.Background(), ge1.UUID, ge1.Name)
	c.Check(err, qt.IsNil)

	ge2 := &dbmodel.GroupEntry{
		Name: "renamed-group",
	}
	err = s.Database.GetGroup(context.Background(), ge2)
	c.Check(err, qt.IsNil)
	c.Assert(ge2, qt.CmpEquals(cmpopts.IgnoreTypes(time.Time{})), ge1)
}

func (s *dbSuite) TestRemoveGroup(c *qt.C) {
	err := s.Database.RemoveGroup(context.Background(), &dbmodel.GroupEntry{Name: "test-group"})
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeNotFound)

	err = s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	ge := &dbmodel.GroupEntry{
		Name: "test-group",
	}
	err = s.Database.RemoveGroup(context.Background(), ge)
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeNotFound)

	groupEntry, err := s.Database.AddGroup(context.Background(), ge.Name)
	c.Assert(err, qt.IsNil)

	ge1 := &dbmodel.GroupEntry{
		Name: "test-group",
	}
	err = s.Database.GetGroup(context.Background(), ge1)
	c.Assert(err, qt.IsNil)
	c.Assert(groupEntry.UUID, qt.Equals, ge1.UUID)

	err = s.Database.RemoveGroup(context.Background(), ge1)
	c.Check(err, qt.IsNil)

	err = s.Database.GetGroup(context.Background(), ge1)
	c.Check(errors.ErrorCode(err), qt.Equals, errors.CodeNotFound)
}

func (s *dbSuite) TestListGroups(c *qt.C) {
	err := s.Database.Migrate(context.Background(), false)
	c.Assert(err, qt.IsNil)

	addNGroups := 10
	for i := range addNGroups {
		_, err := s.Database.AddGroup(context.Background(), fmt.Sprintf("test-group-%d", i))
		c.Assert(err, qt.IsNil)
	}
	ctx := context.Background()
	firstGroups, err := s.Database.ListGroups(ctx, 5, 0, "")
	c.Assert(err, qt.IsNil)
	for i := 0; i < 5; i++ {
		c.Assert(firstGroups[i].Name, qt.Equals, fmt.Sprintf("test-group-%d", i))
	}
	secondGroups, err := s.Database.ListGroups(ctx, 5, 5, "")
	c.Assert(err, qt.IsNil)
	for i := 0; i < 5; i++ {
		c.Assert(secondGroups[i].Name, qt.Equals, fmt.Sprintf("test-group-%d", i+5))
	}

	matchedGroups, err := s.Database.ListGroups(ctx, 5, 0, "group-1")
	c.Assert(err, qt.IsNil)
	c.Assert(matchedGroups, qt.HasLen, 1)
	c.Assert(matchedGroups[0].Name, qt.Equals, "test-group-1")

	matchedGroups, err = s.Database.ListGroups(ctx, 5, 0, "%not-existing%")
	c.Assert(err, qt.IsNil)
	c.Assert(matchedGroups, qt.HasLen, 0)

	tg, err := s.Database.AddGroup(context.Background(), "\\%test-group")
	c.Assert(err, qt.IsNil)

	matchedGroups, err = s.Database.ListGroups(ctx, 5, 0, "\\%t")
	c.Assert(err, qt.IsNil)
	c.Assert(matchedGroups, qt.HasLen, 1)
	c.Assert(matchedGroups[0].UUID, qt.Equals, tg.UUID)

	matchedGroups, err = s.Database.ListGroups(ctx, 5, 0, tg.UUID)
	c.Assert(err, qt.IsNil)
	c.Assert(matchedGroups, qt.HasLen, 1)
	c.Assert(matchedGroups[0].UUID, qt.Equals, tg.UUID)
}
