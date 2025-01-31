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

func TestGetResources(t *testing.T) {
	c := qt.New(t)
	ctx := context.Background()

	j := jimmtest.NewJIMM(c, nil)

	_, _, controller, model, applicationOffer, cloud, _, _ := jimmtest.CreateTestControllerEnvironment(ctx, c, j.Database)

	ids := []string{applicationOffer.UUID, cloud.Name, controller.UUID, model.UUID.String}

	u := openfga.NewUser(&dbmodel.Identity{Name: "admin@canonical.com"}, j.OpenFGAClient)
	u.JimmAdmin = true

	testCases := []struct {
		desc       string
		limit      int
		offset     int
		identities []string
	}{
		{
			desc:       "test with first resources",
			limit:      3,
			offset:     0,
			identities: []string{ids[0], ids[1], ids[2]},
		},
		{
			desc:       "test with remianing ids",
			limit:      3,
			offset:     3,
			identities: []string{ids[3]},
		},
		{
			desc:       "test out of range",
			limit:      3,
			offset:     6,
			identities: []string{},
		},
	}
	for _, t := range testCases {
		c.Run(t.desc, func(c *qt.C) {
			filter := pagination.NewOffsetFilter(t.limit, t.offset)
			resources, err := j.ListResources(ctx, u, filter, "", "")
			c.Assert(err, qt.IsNil)
			c.Assert(resources, qt.HasLen, len(t.identities))
			for i := range len(t.identities) {
				c.Assert(resources[i].ID.String, qt.Equals, t.identities[i])
			}
		})
	}
}
