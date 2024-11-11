// Copyright 2024 Canonical.
package rebac_admin

var (
	NewGroupService       = newGroupService
	NewidentitiesService  = newidentitiesService
	NewResourcesService   = newResourcesService
	NewEntitlementService = newEntitlementService
	EntitlementsList      = entitlementsList
	Capabilities          = capabilities
)

type GroupsService = groupsService
