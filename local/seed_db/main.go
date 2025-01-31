// Copyright 2024 Canonical.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/google/uuid"
	"github.com/juju/juju/core/crossmodel"
	"github.com/juju/juju/state"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/canonical/jimm/v3/internal/db"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/logger"
)

// A simple script to seed a local database for schema testing.

func main() {
	ctx := context.Background()

	gdb, err := gorm.Open(postgres.Open("postgresql://jimm:jimm@localhost/jimm"), &gorm.Config{
		Logger: &logger.GormLogger{},
	})

	if err != nil {
		fmt.Println("failed to connect to db ", err)
		os.Exit(1)
	}

	db := db.Database{
		DB: gdb,
	}

	err = db.Migrate(ctx, false)
	if err != nil {
		fmt.Println("failed to migrate to db ", err)
		os.Exit(1)
	}

	if _, err = db.AddGroup(ctx, "test-group"); err != nil {
		fmt.Println("failed to add group to db ", err)
		os.Exit(1)
	}

	u, _ := dbmodel.NewIdentity(petname.Generate(2, "-") + "@canonical.com")
	if err = db.DB.Create(u).Error; err != nil {
		fmt.Println("failed to add user to db ", err)
		os.Exit(1)
	}

	cloud := dbmodel.Cloud{
		Name: petname.Generate(2, "-"),
		Type: "aws",
		Regions: []dbmodel.CloudRegion{{
			Name: petname.Generate(2, "-"),
		}},
	}
	if err = db.DB.Create(&cloud).Error; err != nil {
		fmt.Println("failed to add cloud to db ", err)
		os.Exit(1)
	}

	id, _ := uuid.NewRandom()
	controller := dbmodel.Controller{
		Name:        petname.Generate(2, "-"),
		UUID:        id.String(),
		CloudName:   cloud.Name,
		CloudRegion: cloud.Regions[0].Name,
		CloudRegions: []dbmodel.CloudRegionControllerPriority{{
			Priority:      0,
			CloudRegionID: cloud.Regions[0].ID,
		}},
	}
	if err = db.AddController(ctx, &controller); err != nil {
		fmt.Println("failed to add controller to db ", err)
		os.Exit(1)
	}

	cred := dbmodel.CloudCredential{
		Name:              petname.Generate(2, "-"),
		CloudName:         cloud.Name,
		OwnerIdentityName: u.Name,
		AuthType:          "empty",
	}
	if err = db.SetCloudCredential(ctx, &cred); err != nil {
		fmt.Println("failed to add cloud credential to db ", err)
		os.Exit(1)
	}

	model := dbmodel.Model{
		Name: petname.Generate(2, "-"),
		UUID: sql.NullString{
			String: id.String(),
			Valid:  true,
		},
		OwnerIdentityName: u.Name,
		ControllerID:      controller.ID,
		CloudRegionID:     cloud.Regions[0].ID,
		CloudCredentialID: cred.ID,
		Life:              state.Alive.String(),
	}
	if err = db.AddModel(ctx, &model); err != nil {
		fmt.Println("failed to add model to db ", err)
		os.Exit(1)
	}

	offerName := petname.Generate(2, "-")
	offerURL, _ := crossmodel.ParseOfferURL(controller.Name + ":" + u.Name + "/" + model.Name + "." + offerName)
	offer := dbmodel.ApplicationOffer{
		UUID:    id.String(),
		Name:    offerName,
		ModelID: model.ID,
		URL:     offerURL.String(),
	}
	if err = db.AddApplicationOffer(context.Background(), &offer); err != nil {
		fmt.Println("failed to add application offer to db ", err)
		os.Exit(1)
	}

	fmt.Println("DB seeded.")
}
