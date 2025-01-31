// Copyright 2024 Canonical.

package rebac_admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/canonical/ofga"
	rebac_handlers "github.com/canonical/rebac-admin-ui-handlers/v1"
	"github.com/canonical/rebac-admin-ui-handlers/v1/resources"
	qt "github.com/frankban/quicktest"
	"github.com/google/uuid"

	"github.com/canonical/jimm/v3/internal/common/pagination"
	"github.com/canonical/jimm/v3/internal/common/utils"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	jimmm_errors "github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/jimm"
	"github.com/canonical/jimm/v3/internal/jimmhttp/rebac_admin"
	"github.com/canonical/jimm/v3/internal/openfga"
	ofganames "github.com/canonical/jimm/v3/internal/openfga/names"
	"github.com/canonical/jimm/v3/internal/testutils/jimmtest"
	"github.com/canonical/jimm/v3/internal/testutils/jimmtest/mocks"
	"github.com/canonical/jimm/v3/pkg/api/params"
)

func TestGetIdentity(t *testing.T) {
	c := qt.New(t)
	jimm := jimmtest.JIMM{
		FetchIdentity_: func(ctx context.Context, username string) (*openfga.User, error) {
			if username == "bob@canonical.com" {
				return openfga.NewUser(&dbmodel.Identity{Name: "bob@canonical.com"}, nil), nil
			}
			return nil, jimmm_errors.E(jimmm_errors.CodeNotFound)
		},
	}
	user := openfga.User{}
	user.JimmAdmin = true
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	identitySvc := rebac_admin.NewidentitiesService(&jimm)

	// test with user found
	identity, err := identitySvc.GetIdentity(ctx, "bob@canonical.com")
	c.Assert(err, qt.IsNil)
	c.Assert(identity.Email, qt.Equals, "bob@canonical.com")

	// test with user not found
	_, err = identitySvc.GetIdentity(ctx, "bob-not-found@canonical.com")
	c.Assert(err, qt.ErrorMatches, "Not Found: User with id bob-not-found@canonical.com not found")
}

func TestListIdentities(t *testing.T) {
	testUsers := []openfga.User{
		*openfga.NewUser(&dbmodel.Identity{Name: "bob0@canonical.com"}, nil),
		*openfga.NewUser(&dbmodel.Identity{Name: "bob1@canonical.com"}, nil),
		*openfga.NewUser(&dbmodel.Identity{Name: "bob2@canonical.com"}, nil),
		*openfga.NewUser(&dbmodel.Identity{Name: "bob3@canonical.com"}, nil),
		*openfga.NewUser(&dbmodel.Identity{Name: "bob4@canonical.com"}, nil),
	}
	c := qt.New(t)
	jimm := jimmtest.JIMM{
		ListIdentities_: func(ctx context.Context, user *openfga.User, pagination pagination.LimitOffsetPagination, match string) ([]openfga.User, error) {
			start := pagination.Offset()
			end := start + pagination.Limit()
			if end > len(testUsers) {
				end = len(testUsers)
			}
			return testUsers[start:end], nil
		},
		CountIdentities_: func(ctx context.Context, user *openfga.User) (int, error) {
			return len(testUsers), nil
		},
	}
	user := openfga.User{}
	user.JimmAdmin = true
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	identitySvc := rebac_admin.NewidentitiesService(&jimm)

	testCases := []struct {
		desc         string
		size         *int
		page         *int
		wantPage     int
		wantSize     int
		wantTotal    int
		wantNextpage *int
		emails       []string
	}{
		{
			desc:         "test with first page",
			size:         utils.IntToPointer(2),
			page:         utils.IntToPointer(0),
			wantPage:     0,
			wantSize:     2,
			wantNextpage: utils.IntToPointer(1),
			wantTotal:    len(testUsers),
			emails:       []string{testUsers[0].Name, testUsers[1].Name},
		},
		{
			desc:         "test with second page",
			size:         utils.IntToPointer(2),
			page:         utils.IntToPointer(1),
			wantPage:     1,
			wantSize:     2,
			wantNextpage: utils.IntToPointer(2),
			wantTotal:    len(testUsers),
			emails:       []string{testUsers[2].Name, testUsers[3].Name},
		},
		{
			desc:         "test with last page",
			size:         utils.IntToPointer(2),
			page:         utils.IntToPointer(2),
			wantPage:     2,
			wantSize:     1,
			wantNextpage: nil,
			wantTotal:    len(testUsers),
			emails:       []string{testUsers[4].Name},
		},
	}
	for _, t := range testCases {
		c.Run(t.desc, func(c *qt.C) {
			identities, err := identitySvc.ListIdentities(ctx, &resources.GetIdentitiesParams{
				Size: t.size,
				Page: t.page,
			})
			c.Assert(err, qt.IsNil)
			c.Assert(*identities.Meta.Page, qt.Equals, t.wantPage)
			c.Assert(identities.Meta.Size, qt.Equals, t.wantSize)
			if t.wantNextpage == nil {
				c.Assert(identities.Next.Page, qt.IsNil)
			} else {
				c.Assert(*identities.Next.Page, qt.Equals, *t.wantNextpage)
			}
			c.Assert(*identities.Meta.Total, qt.Equals, t.wantTotal)
			c.Assert(identities.Data, qt.HasLen, len(t.emails))
			for i := range len(t.emails) {
				c.Assert(identities.Data[i].Email, qt.Equals, t.emails[i])
			}
		})
	}
}

func TestGetIdentityGroups(t *testing.T) {
	c := qt.New(t)
	var listTuplesErr error
	testTuple := openfga.Tuple{
		Object:   &ofga.Entity{Kind: "user", ID: "foo"},
		Relation: ofga.Relation("member"),
		Target:   &ofga.Entity{Kind: "group", ID: "my-group-id"},
	}
	groupManager := mocks.GroupManager{
		GetGroupByUUID_: func(ctx context.Context, user *openfga.User, uuid string) (*dbmodel.GroupEntry, error) {
			return &dbmodel.GroupEntry{Name: "fake-group-name"}, nil
		},
	}
	jimm := jimmtest.JIMM{
		FetchIdentity_: func(ctx context.Context, username string) (*openfga.User, error) {
			if username == "bob@canonical.com" {
				return openfga.NewUser(&dbmodel.Identity{Name: "bob@canonical.com"}, nil), nil
			}
			return nil, dbmodel.IdentityCreationError
		},
		RelationService: mocks.RelationService{
			ListRelationshipTuples_: func(ctx context.Context, user *openfga.User, tuple params.RelationshipTuple, pageSize int32, continuationToken string) ([]openfga.Tuple, string, error) {
				return []openfga.Tuple{testTuple}, "continuation-token", listTuplesErr
			},
		},
		GroupManager_: func() jimm.GroupManager {
			return &groupManager
		},
	}
	user := openfga.User{}
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	idSvc := rebac_admin.NewidentitiesService(&jimm)

	_, err := idSvc.GetIdentityGroups(ctx, "bob-not-found@canonical.com", &resources.GetIdentitiesItemGroupsParams{})
	c.Assert(err, qt.ErrorMatches, ".*not found")
	username := "bob@canonical.com"

	res, err := idSvc.GetIdentityGroups(ctx, username, &resources.GetIdentitiesItemGroupsParams{})
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.IsNotNil)
	c.Assert(res.Data, qt.HasLen, 1)
	c.Assert(*res.Data[0].Id, qt.Equals, "my-group-id")
	c.Assert(res.Data[0].Name, qt.Equals, "fake-group-name")
	c.Assert(*res.Next.PageToken, qt.Equals, "continuation-token")

	listTuplesErr = errors.New("foo")
	_, err = idSvc.GetIdentityGroups(ctx, username, &resources.GetIdentitiesItemGroupsParams{})
	c.Assert(err, qt.ErrorMatches, "foo")
}

func TestPatchIdentityGroups(t *testing.T) {
	c := qt.New(t)
	var patchTuplesErr error
	jimm := jimmtest.JIMM{
		FetchIdentity_: func(ctx context.Context, username string) (*openfga.User, error) {
			if username == "bob@canonical.com" {
				return openfga.NewUser(&dbmodel.Identity{Name: "bob@canonical.com"}, nil), nil
			}
			return nil, dbmodel.IdentityCreationError
		},
		RelationService: mocks.RelationService{
			AddRelation_: func(ctx context.Context, user *openfga.User, tuples []params.RelationshipTuple) error {
				return patchTuplesErr
			},
			RemoveRelation_: func(ctx context.Context, user *openfga.User, tuples []params.RelationshipTuple) error {
				return patchTuplesErr
			},
		},
	}
	user := openfga.User{}
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	idSvc := rebac_admin.NewidentitiesService(&jimm)

	_, err := idSvc.PatchIdentityGroups(ctx, "bob-not-found@canonical.com", nil)
	c.Assert(err, qt.ErrorMatches, ".* not found")

	username := "bob@canonical.com"
	group1ID := uuid.New()
	group2ID := uuid.New()
	operations := []resources.IdentityGroupsPatchItem{
		{Group: group1ID.String(), Op: resources.IdentityGroupsPatchItemOpAdd},
		{Group: group2ID.String(), Op: resources.IdentityGroupsPatchItemOpRemove},
	}
	res, err := idSvc.PatchIdentityGroups(ctx, username, operations)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.IsTrue)

	patchTuplesErr = errors.New("foo")
	_, err = idSvc.PatchIdentityGroups(ctx, username, operations)
	c.Assert(err, qt.ErrorMatches, ".*foo")

	invalidGroupName := []resources.IdentityGroupsPatchItem{
		{Group: "test-group1", Op: resources.IdentityGroupsPatchItemOpAdd},
	}
	_, err = idSvc.PatchIdentityGroups(ctx, "bob@canonical.com", invalidGroupName)
	c.Assert(err, qt.ErrorMatches, "Bad Request: ID test-group1 is not a valid group ID")
}

func TestGetIdentityRoles(t *testing.T) {
	c := qt.New(t)
	var listTuplesErr error
	testTuple := openfga.Tuple{
		Object:   &ofga.Entity{Kind: "user", ID: "foo"},
		Relation: ofganames.AssigneeRelation,
		Target:   &ofga.Entity{Kind: "role", ID: "my-role-id"},
	}
	roleManager := mocks.RoleManager{
		GetRoleByUUID_: func(ctx context.Context, user *openfga.User, uuid string) (*dbmodel.RoleEntry, error) {
			return &dbmodel.RoleEntry{Name: "fake-role-name"}, nil
		},
	}
	jimm := jimmtest.JIMM{
		FetchIdentity_: func(ctx context.Context, username string) (*openfga.User, error) {
			if username == "bob@canonical.com" {
				return openfga.NewUser(&dbmodel.Identity{Name: "bob@canonical.com"}, nil), nil
			}
			return nil, dbmodel.IdentityCreationError
		},
		RelationService: mocks.RelationService{
			ListRelationshipTuples_: func(ctx context.Context, user *openfga.User, tuple params.RelationshipTuple, pageSize int32, continuationToken string) ([]openfga.Tuple, string, error) {
				return []openfga.Tuple{testTuple}, "continuation-token", listTuplesErr
			},
		},
		RoleManager_: func() jimm.RoleManager {
			return roleManager
		},
	}
	user := openfga.User{}
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	idSvc := rebac_admin.NewidentitiesService(&jimm)

	_, err := idSvc.GetIdentityRoles(ctx, "bob-not-found@canonical.com", &resources.GetIdentitiesItemRolesParams{})
	c.Assert(err, qt.ErrorMatches, ".*not found")
	username := "bob@canonical.com"

	res, err := idSvc.GetIdentityRoles(ctx, username, &resources.GetIdentitiesItemRolesParams{})
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.IsNotNil)
	c.Assert(res.Data, qt.HasLen, 1)
	c.Assert(*res.Data[0].Id, qt.Equals, "my-role-id")
	c.Assert(res.Data[0].Name, qt.Equals, "fake-role-name")
	c.Assert(*res.Next.PageToken, qt.Equals, "continuation-token")

	listTuplesErr = errors.New("foo")
	_, err = idSvc.GetIdentityRoles(ctx, username, &resources.GetIdentitiesItemRolesParams{})
	c.Assert(err, qt.ErrorMatches, "foo")
}

func TestPatchIdentityRoles(t *testing.T) {
	c := qt.New(t)
	var patchTuplesErr error
	jimm := jimmtest.JIMM{
		FetchIdentity_: func(ctx context.Context, username string) (*openfga.User, error) {
			if username == "bob@canonical.com" {
				return openfga.NewUser(&dbmodel.Identity{Name: "bob@canonical.com"}, nil), nil
			}
			return nil, dbmodel.IdentityCreationError
		},
		RelationService: mocks.RelationService{
			AddRelation_: func(ctx context.Context, user *openfga.User, tuples []params.RelationshipTuple) error {
				return patchTuplesErr
			},
			RemoveRelation_: func(ctx context.Context, user *openfga.User, tuples []params.RelationshipTuple) error {
				return patchTuplesErr
			},
		},
	}
	user := openfga.User{}
	ctx := context.Background()
	ctx = rebac_handlers.ContextWithIdentity(ctx, &user)
	idSvc := rebac_admin.NewidentitiesService(&jimm)

	_, err := idSvc.PatchIdentityRoles(ctx, "bob-not-found@canonical.com", nil)
	c.Assert(err, qt.ErrorMatches, ".* not found")

	username := "bob@canonical.com"
	role1ID := uuid.New()
	role2ID := uuid.New()
	operations := []resources.IdentityRolesPatchItem{
		{Role: role1ID.String(), Op: resources.IdentityRolesPatchItemOpAdd},
		{Role: role2ID.String(), Op: resources.IdentityRolesPatchItemOpRemove},
	}
	res, err := idSvc.PatchIdentityRoles(ctx, username, operations)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.IsTrue)

	patchTuplesErr = errors.New("foo")
	_, err = idSvc.PatchIdentityRoles(ctx, username, operations)
	c.Assert(err, qt.ErrorMatches, ".*foo")

	invalidRoleName := []resources.IdentityRolesPatchItem{
		{Role: "test-role1", Op: resources.IdentityRolesPatchItemOpAdd},
	}
	_, err = idSvc.PatchIdentityRoles(ctx, "bob@canonical.com", invalidRoleName)
	c.Assert(err, qt.ErrorMatches, "Bad Request: ID test-role1 is not a valid role ID")
}
