// Copyright 2024 Canonical.

package jimm_test

import (
	"context"
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/canonical/jimm/v3/internal/common/pagination"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/openfga"
	"github.com/canonical/jimm/v3/internal/testutils/jimmtest"
)

func TestFetchIdentity(t *testing.T) {
	c := qt.New(t)
	ctx := context.Background()

	j := jimmtest.NewJIMM(c, nil)

	user, _, _, _, _, _, _, _ := jimmtest.CreateTestControllerEnvironment(ctx, c, j.Database)
	u, err := j.FetchIdentity(ctx, user.Name)
	c.Assert(err, qt.IsNil)
	c.Assert(u.Name, qt.Equals, user.Name)

	_, err = j.FetchIdentity(ctx, "bobnotfound@canonical.com")
	c.Assert(err, qt.ErrorMatches, "record not found")
}

func TestListIdentities(t *testing.T) {
	c := qt.New(t)
	ctx := context.Background()

	j := jimmtest.NewJIMM(c, nil)

	u := openfga.NewUser(&dbmodel.Identity{Name: "admin@canonical.com"}, j.OpenFGAClient)
	u.JimmAdmin = true

	pag := pagination.NewOffsetFilter(10, 0)
	users, err := j.ListIdentities(ctx, u, pag, "")
	c.Assert(err, qt.IsNil)
	c.Assert(len(users), qt.Equals, 0)

	userNames := []string{
		"bob1@canonical.com",
		"bob3@canonical.com",
		"bob5@canonical.com",
		"bob4@canonical.com",
	}
	// add users
	for _, name := range userNames {
		_, err := j.GetUser(ctx, name)
		c.Assert(err, qt.IsNil)
	}

	testCases := []struct {
		desc       string
		limit      int
		offset     int
		match      string
		identities []string
	}{
		{
			desc:       "test with first ids",
			limit:      3,
			offset:     0,
			identities: []string{userNames[0], userNames[1], userNames[3]},
		},
		{
			desc:       "test with remianing ids",
			limit:      3,
			offset:     3,
			identities: []string{userNames[2]},
		},
		{
			desc:       "test out of range",
			limit:      3,
			offset:     6,
			identities: []string{},
		},
		{
			desc:       "test with match",
			limit:      5,
			offset:     0,
			identities: []string{userNames[0]},
			match:      "bob1",
		},
	}
	for _, t := range testCases {
		c.Run(t.desc, func(c *qt.C) {
			pag = pagination.NewOffsetFilter(t.limit, t.offset)
			identities, err := j.ListIdentities(ctx, u, pag, t.match)
			c.Assert(err, qt.IsNil)
			c.Assert(identities, qt.HasLen, len(t.identities))
			for i := range len(t.identities) {
				c.Assert(identities[i].Name, qt.Equals, t.identities[i])
			}
		})
	}
}

func TestCountIdentities(t *testing.T) {
	c := qt.New(t)
	ctx := context.Background()

	j := jimmtest.NewJIMM(c, nil)

	u := openfga.NewUser(&dbmodel.Identity{Name: "admin@canonical.com"}, j.OpenFGAClient)
	u.JimmAdmin = true

	userNames := []string{
		"bob1@canonical.com",
		"bob3@canonical.com",
		"bob5@canonical.com",
		"bob4@canonical.com",
	}
	// add users
	for _, name := range userNames {
		_, err := j.GetUser(ctx, name)
		c.Assert(err, qt.IsNil)
	}
	count, err := j.CountIdentities(ctx, u)
	c.Assert(err, qt.IsNil)
	c.Assert(count, qt.Equals, 4)
}
