// Copyright 2024 Canonical.
package jimmjwx_test

import (
	"context"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/canonical/jimm/v3/internal/jimm/credentials"
	"github.com/canonical/jimm/v3/internal/jimmjwx"
	"github.com/canonical/jimm/v3/internal/testutils/jimmtest"
	"github.com/canonical/jimm/v3/internal/vault"
)

func newStore(t testing.TB) *vault.VaultStore {
	client, path, roleID, roleSecretID, ok := jimmtest.VaultClient(t)
	if !ok {
		t.Skip("vault not available")
	}
	return &vault.VaultStore{
		Client:       client,
		RoleID:       roleID,
		RoleSecretID: roleSecretID,
		KVPath:       path,
	}
}

func getJWKS(c *qt.C) jwk.Set {
	set, err := jwk.ParseString(`
	{
		"keys": [
		  {
			"alg": "RS256",
			"kty": "RSA",
			"use": "sig",
			"n": "yeNlzlub94YgerT030codqEztjfU_S6X4DbDA_iVKkjAWtYfPHDzz_sPCT1Axz6isZdf3lHpq_gYX4Sz-cbe4rjmigxUxr-FgKHQy3HeCdK6hNq9ASQvMK9LBOpXDNn7mei6RZWom4wo3CMvvsY1w8tjtfLb-yQwJPltHxShZq5-ihC9irpLI9xEBTgG12q5lGIFPhTl_7inA1PFK97LuSLnTJzW0bj096v_TMDg7pOWm_zHtF53qbVsI0e3v5nmdKXdFf9BjIARRfVrbxVxiZHjU6zL6jY5QJdh1QCmENoejj_ytspMmGW7yMRxzUqgxcAqOBpVm0b-_mW3HoBdjQ",
			"e": "AQAB",
			"kid": "32d2b213-d3fe-436c-9d4c-67a673890620"
		  }
		]
	}
	`)
	c.Assert(err, qt.IsNil)
	return set
}

// startTestRotator starts a rotator, returning the ks that has been found
// it does not guarantee the keyset has any keys!
func startAndTestRotator(c *qt.C, ctx context.Context, store credentials.CredentialStore, svc *jimmjwx.JWKSService) jwk.Set {
	err := store.CleanupJWKS(ctx)
	c.Assert(err, qt.IsNil)

	tick := make(chan time.Time, 1)
	tick <- time.Now()
	err = svc.StartJWKSRotator(ctx, tick, time.Now().AddDate(0, 3, 0))
	c.Assert(err, qt.IsNil)

	var ks jwk.Set
	// We retry 500ms * 60 (30s)
	for i := 0; i < 60; i++ {
		if ks == nil {
			ks, err = store.GetJWKS(ctx)
			if err != nil {
				c.Logf("failed to get JWKS: %s", err)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break

	}
	c.Assert(err, qt.IsNil)
	key, ok := ks.Key(0)
	c.Assert(ok, qt.IsTrue)
	_, err = uuid.Parse(key.KeyID())
	c.Assert(err, qt.IsNil)
	return ks
}

// setupCredentialStore sets up a credential store with the correct params to connect to vault. It also ensures
// that vault is wiped each time this is called.
func setupCredentialStore(ctx context.Context, c *qt.C) credentials.CredentialStore {
	store := newStore(c)
	// Ensure store is wiped
	err := store.CleanupJWKS(ctx)
	c.Assert(err, qt.IsNil)

	_, _, _, _, ok := jimmtest.VaultClient(c)
	c.Assert(ok, qt.IsTrue)

	return store
}
