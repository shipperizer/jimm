// Copyright 2024 Canonical.

// service defines the methods necessary to start a JIMM server
// alongside all the config options that can be supplied to configure JIMM.
package service

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/antonlindstrom/pgstore"
	service "github.com/canonical/go-service"
	cofga "github.com/canonical/ofga"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	vaultapi "github.com/hashicorp/vault/api"
	"github.com/juju/names/v5"
	"github.com/juju/zaputil/zapctx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/canonical/jimm/v3/internal/auth"
	"github.com/canonical/jimm/v3/internal/db"
	"github.com/canonical/jimm/v3/internal/dbmodel"
	"github.com/canonical/jimm/v3/internal/discharger"
	"github.com/canonical/jimm/v3/internal/errors"
	"github.com/canonical/jimm/v3/internal/jimm"
	jimmcreds "github.com/canonical/jimm/v3/internal/jimm/credentials"
	"github.com/canonical/jimm/v3/internal/jimmhttp"
	"github.com/canonical/jimm/v3/internal/jimmhttp/rebac_admin"
	"github.com/canonical/jimm/v3/internal/jimmjwx"
	"github.com/canonical/jimm/v3/internal/jujuapi"
	"github.com/canonical/jimm/v3/internal/jujuclient"
	"github.com/canonical/jimm/v3/internal/logger"
	"github.com/canonical/jimm/v3/internal/middleware"
	"github.com/canonical/jimm/v3/internal/openfga"
	ofganames "github.com/canonical/jimm/v3/internal/openfga/names"
	"github.com/canonical/jimm/v3/internal/pubsub"
	"github.com/canonical/jimm/v3/internal/vault"
)

const (
	localDischargePath = "/macaroons"
)

// OpenFGAParams holds parameters needed to connect to the OpenFGA server.
type OpenFGAParams struct {
	Scheme    string
	Host      string
	Store     string
	AuthModel string
	Token     string
	Port      string
}

// OAuthAuthenticatorParams holds parameters needed to configure an OAuthAuthenticator
// implementation.
type OAuthAuthenticatorParams struct {
	// IssuerURL is the URL of the OAuth2.0 server.
	// I.e., http://localhost:8082/realms/jimm in the case of keycloak.
	IssuerURL string

	// ClientID holds the OAuth2.0. The client IS expected to be confidential.
	ClientID string

	// ClientSecret holds the OAuth2.0 "client-secret" to authenticate when performing
	// /auth and /token requests.
	ClientSecret string

	// Scopes holds the scopes that you wish to retrieve.
	Scopes []string

	// SessionTokenExpiry holds the expiry duration for issued JWTs
	// for user (CLI) to JIMM authentication.
	SessionTokenExpiry time.Duration

	// SessionCookieMaxAge holds the max age for session cookies in seconds.
	SessionCookieMaxAge int

	// SecureSessionCookies determines if HTTPS must be enabled in order for JIMM
	// to set cookies when creating browser based sessions.
	SecureSessionCookies bool

	// JWTSessionKey holds the secret key used for signing/verifying JWT tokens.
	// See internal/auth/oauth2.go AuthenticationService.SessionSecretkey for more details.
	JWTSessionKey string
}

// A Params structure contains the parameters required to initialise a new
// Service.
type Params struct {
	// ControllerUUID contains the UUID of the JIMM controller, if this
	// is not set a random UUID will be generated.
	ControllerUUID string

	// IsLeader indicates that this is the JIMM leader unit.
	IsLeader bool

	// DSN is the data source name that the JIMM service will use to
	// connect to its database. If this is empty an in-memory database
	// will be used.
	DSN string

	// ControllerAdmins contains a list of users (or groups)
	// that will be given the access-level "superuser" when they
	// authenticate to the controller.
	ControllerAdmins []string

	// DisableConnectionCache disables caching connections to
	// controllers. By default controller connections are cached, if
	// this is set then a new connection will be created for each API
	// call. This is mostly useful for testing.
	DisableConnectionCache bool

	// VaultRoleID is the AppRole role ID.
	VaultRoleID string

	// VaultRoleSecretID is the AppRole secret ID.
	VaultRoleSecretID string

	// VaultAddress is the URL of a vault server that will be used to
	// store secrets for JIMM. If this is empty then the default
	// address of the vault server is used.
	VaultAddress string

	// VaultAuthPath is the path on the vault server that JIMM will use
	// to attempt to authenticate using the credentials in the
	// VaultSecretFile. If this is empty then authentication is not
	// attempted and the VaultSecretFile must contain token that can be
	// used directly.
	VaultAuthPath string

	// VaultPath is the path on the vault server which hosts the kv
	// secrets engine JIMM will use to store secrets.
	VaultPath string

	// PublicDNSName is the name to advertise as the public address of
	// the juju controller.
	PublicDNSName string

	// Parameters used to initialize connection to an OpenFGA server.
	OpenFGAParams OpenFGAParams

	// PrivateKey holds the private part of the bakery keypair.
	PrivateKey string

	// PublicKey holds the public part of the bakery keypair.
	PublicKey string

	// auditLogRetentionPeriodInDays is the number of days detailing how long
	// to keep an audit log for before purging it from the database.
	AuditLogRetentionPeriodInDays string

	// MacaroonExpiryDuration holds the expiry duration of authentication macaroons.
	MacaroonExpiryDuration time.Duration

	// JWTExpiryDuration holds the expiry duration for issued JWTs
	// for controller to JIMM communication ONLY.
	JWTExpiryDuration time.Duration

	// InsecureSecretStorage instructs JIMM to store secrets in its database
	// instead of dedicated secure storage. SHOULD NOT BE USED IN PRODUCTION.
	InsecureSecretStorage bool

	// OAuthAuthenticatorParams holds parameters needed to configure an OAuthAuthenticator
	// implementation.
	OAuthAuthenticatorParams OAuthAuthenticatorParams

	// DashboardFinalRedirectURL is the URL to FINALLY redirect to after completing
	// the /callback in an authorisation code OAuth2.0 flow to finish the flow.
	DashboardFinalRedirectURL string

	// CookieSessionKey is a randomly generated secret passed via config used for signing
	// cookie data. The recommended length is 32/64 characters from the Gorilla securecookie lib.
	// https://github.com/gorilla/securecookie/blob/main/securecookie.go#L124
	CookieSessionKey []byte

	// CorsAllowedOrigins represents all addresses that are valid for cross-origin
	// requests. A wildcard '*' is accepted to allow all cross-origin requests.
	CorsAllowedOrigins []string

	// LogSQL determines whether ORM queries are printed when debug logs are enabled.
	// This may leak secrets in logs when sensitive values are stored in the DB like OAuth tokens.
	LogSQL bool

	// LogLevel is the default logger is set.
	// Setting this to "debug" enables the requests logger as well.
	LogLevel string
}

// A Service is the implementation of a JIMM server.
type Service struct {
	jimm       *jimm.JIMM
	jwkService *jimmjwx.JWKSService

	isLeader              bool
	auditLogCleanupPeriod int

	mux      *chi.Mux
	cleanups []func() error
}

func (s *Service) JIMM() *jimm.JIMM {
	return s.jimm
}

// ServeHTTP implements http.Handler.
func (s *Service) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.mux.ServeHTTP(w, req)
}

// WatchModelSummaries connects to all controllers and starts a
// ModelSummaryWatcher for all models. WatchModelSummaries finishes when
// the given context is canceled, or there is a fatal error watching model
// summaries.
func (s *Service) WatchModelSummaries(ctx context.Context) error {
	w := jimm.Watcher{
		Database: s.jimm.Database,
		Dialer:   s.jimm.Dialer,
		Pubsub:   s.jimm.Pubsub,
	}
	return w.WatchAllModelSummaries(ctx, 10*time.Minute)
}

// StartJWKSRotator see internal/jimmjwx/jwks.go for details.
func (s *Service) StartJWKSRotator(ctx context.Context, checkRotateRequired <-chan time.Time, initialRotateRequiredTime time.Time) error {
	return s.jwkService.StartJWKSRotator(ctx, checkRotateRequired, initialRotateRequiredTime)
}

// MonitorResources periodically updates metrics.
func (s *Service) MonitorResources(ctx context.Context) {
	s.jimm.UpdateMetrics(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ticker.C:
			s.jimm.UpdateMetrics(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// OpenFGACleanup starts a goroutine that cleans up any orphaned tuples from OpenFGA.
func (s *Service) OpenFGACleanup(ctx context.Context, trigger <-chan time.Time) error {
	for {
		select {
		case <-trigger:
			err := s.jimm.OpenFGACleanup(ctx)
			if err != nil {
				zapctx.Error(ctx, "openfga cleanup", zap.Error(err))
				continue
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// CleanupDyingModels triggers every `trigger` time and calls the jimm methods to cleanup dying models.
func (s *Service) CleanupDyingModels(ctx context.Context, trigger <-chan time.Time) error {
	for {
		select {
		case <-trigger:
			err := s.jimm.CleanupDyingModels(ctx)
			if err != nil {
				zapctx.Error(ctx, "dying models cleanup", zap.Error(err))
				continue
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// Cleanup cleans up resources that need to be released on shutdown.
func (s *Service) Cleanup() {
	// Iterating over clean up function in reverse-order to avoid early clean ups.
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		f := s.cleanups[i]
		if err := f(); err != nil {
			zapctx.Error(context.Background(), "cleanup failed", zap.Error(err))
		}
	}
}

// AddCleanup adds a clean up function to be run at service shutdown.
func (s *Service) AddCleanup(f func() error) {
	s.cleanups = append(s.cleanups, f)
}

// NewService creates a new Service using the given params.
//
//nolint:gocognit // NewService function to be ignored.
func NewService(ctx context.Context, p Params) (*Service, error) {
	const op = errors.Op("NewService")

	s := new(Service)

	jimmParameters := jimm.Parameters{
		UUID:   p.ControllerUUID,
		Pubsub: &pubsub.Hub{MaxConcurrency: 50},
	}
	// Setup all dependency services
	if jimmParameters.UUID == "" {
		jimmParameters.UUID = uuid.NewString()
	}

	if p.DSN == "" {
		return nil, errors.E(op, "missing DSN")
	}

	database, err := openDB(ctx, p.DSN, p.LogSQL)
	if err != nil {
		return nil, errors.E(op, err)
	}
	db := &db.Database{
		DB: database,
	}
	jimmParameters.Database = db

	openFGAclient, err := newOpenFGAClient(ctx, p.OpenFGAParams)
	if err != nil {
		return nil, errors.E(op, err)
	}
	jimmParameters.OpenFGAClient = openFGAclient

	if err := ensureControllerAdministrators(ctx, openFGAclient, p.ControllerUUID, p.ControllerAdmins); err != nil {
		return nil, errors.E(op, err, "failed to ensure controller admins")
	}

	credentialStore, err := s.setupCredentialStore(ctx, p, db)
	if err != nil {
		return nil, errors.E(op, err)
	}
	jimmParameters.CredentialStore = credentialStore

	sessionStore, err := s.setupSessionStore(ctx, p.CookieSessionKey, db)
	if err != nil {
		return nil, errors.E(op, err)
	}
	s.AddCleanup(func() error {
		sessionStore.Close()
		return nil
	})

	redirectUrl := p.PublicDNSName + jimmhttp.AuthResourceBasePath + jimmhttp.CallbackEndpoint
	if !strings.HasPrefix(redirectUrl, "https://") || !strings.HasPrefix(redirectUrl, "http://") {
		redirectUrl = "https://" + redirectUrl
	}

	authSvc, err := auth.NewAuthenticationService(
		ctx,
		auth.AuthenticationServiceParams{
			IssuerURL:           p.OAuthAuthenticatorParams.IssuerURL,
			ClientID:            p.OAuthAuthenticatorParams.ClientID,
			ClientSecret:        p.OAuthAuthenticatorParams.ClientSecret,
			Scopes:              p.OAuthAuthenticatorParams.Scopes,
			SessionTokenExpiry:  p.OAuthAuthenticatorParams.SessionTokenExpiry,
			SessionCookieMaxAge: p.OAuthAuthenticatorParams.SessionCookieMaxAge,
			JWTSessionKey:       p.OAuthAuthenticatorParams.JWTSessionKey,
			SecureCookies:       p.OAuthAuthenticatorParams.SecureSessionCookies,
			Store:               db,
			SessionStore:        sessionStore,
			RedirectURL:         redirectUrl,
		},
	)
	jimmParameters.OAuthAuthenticator = authSvc
	if err != nil {
		zapctx.Error(ctx, "failed to setup authentication service", zap.Error(err))
		return nil, errors.E(op, err, "failed to setup authentication service")
	}

	if p.JWTExpiryDuration == 0 {
		p.JWTExpiryDuration = 24 * time.Hour
	}

	s.jwkService = jimmjwx.NewJWKSService(credentialStore)
	jimmParameters.JWTService = jimmjwx.NewJWTService(jimmjwx.JWTServiceParams{
		Host:   p.PublicDNSName,
		Store:  credentialStore,
		Expiry: p.JWTExpiryDuration,
	})
	jimmParameters.Dialer = &jujuclient.Dialer{
		ControllerCredentialsStore: credentialStore,
		JWTService:                 jimmParameters.JWTService,
	}

	if !p.DisableConnectionCache {
		jimmParameters.Dialer = jimm.CacheDialer(jimmParameters.Dialer)
	}

	if _, err := url.Parse(p.DashboardFinalRedirectURL); err != nil {
		return nil, errors.E(op, err, "failed to parse final redirect url for the dashboard")
	}

	// instantiate jimm
	s.jimm, err = jimm.New(jimmParameters)
	if err != nil {
		return nil, errors.E(op, err)
	}

	s.mux = chi.NewRouter()

	s.mux.Use(chimiddleware.RequestLogger(&logger.HTTPLogFormatter{}))
	s.mux.Use(middleware.MeasureHTTPResponseTime)

	// Setup CORS middleware
	corsOpts := cors.New(cors.Options{
		AllowedOrigins:   p.CorsAllowedOrigins,
		AllowedMethods:   []string{"GET"},
		AllowCredentials: true,
	})
	s.mux.Use(corsOpts.Handler)

	// Setup all HTTP handlers.
	mountHandler := func(path string, h jimmhttp.JIMMHttpHandler) {
		s.mux.Mount(path, h.Routes())
	}

	s.mux.Mount("/metrics", promhttp.Handler())

	rebacBackend, err := rebac_admin.SetupBackend(ctx, s.jimm)
	if err != nil {
		return nil, errors.E(op, err)
	}

	s.mux.Mount("/rebac", middleware.AuthenticateRebac("/rebac", rebacBackend.Handler(""), s.jimm))

	mountHandler(
		"/debug",
		jimmhttp.NewDebugHandler(
			map[string]jimmhttp.StatusCheck{
				"start_time": jimmhttp.ServerStartTime,
			},
		),
	)
	mountHandler(
		"/.well-known",
		jimmhttp.NewWellKnownHandler(s.jimm.CredentialStore),
	)

	if p.DashboardFinalRedirectURL == "" {
		zapctx.Warn(ctx, "OAuth handler not enabled, due to unset dashboard redirect URL")
	} else {
		oauthHandler, err := jimmhttp.NewOAuthHandler(jimmhttp.OAuthHandlerParams{
			Authenticator:             authSvc,
			DashboardFinalRedirectURL: p.DashboardFinalRedirectURL,
		})
		if err != nil {
			zapctx.Error(ctx, "failed to setup authentication handler", zap.Error(err))
			return nil, errors.E(op, err, "failed to setup authentication handler")
		}
		mountHandler(
			jimmhttp.AuthResourceBasePath,
			oauthHandler,
		)
	}

	macaroonDischarger, err := s.setupDischarger(p)
	if err != nil {
		return nil, errors.E(op, err, "failed to set up discharger")
	}
	s.mux.Handle(localDischargePath+"/*", discharger.GetDischargerMux(macaroonDischarger, localDischargePath))

	params := jujuapi.Params{
		ControllerUUID: p.ControllerUUID,
		PublicDNSName:  p.PublicDNSName,
	}

	// Websockets require extra care when cookies are used for authentication
	// to avoid CSRF attacks. https://portswigger.net/web-security/websockets/cross-site-websocket-hijacking
	websocketCors := middleware.NewWebsocketCors(p.CorsAllowedOrigins)
	s.mux.Handle("/api", websocketCors.Handler(jujuapi.APIHandler(ctx, s.jimm, params)))
	s.mux.Handle("/model/*", websocketCors.Handler(http.StripPrefix("/model", jujuapi.ModelHandler(ctx, s.jimm, params))))
	mountHandler(
		"/model/{uuid}/{type:charms|applications}",
		jimmhttp.NewHTTPProxyHandler(s.jimm),
	)

	if p.AuditLogRetentionPeriodInDays != "" {
		var err error
		s.auditLogCleanupPeriod, err = strconv.Atoi(p.AuditLogRetentionPeriodInDays)
		if err != nil {
			return nil, errors.E(op, "failed to parse audit log retention period")
		}
		if s.auditLogCleanupPeriod < 0 {
			return nil, errors.E(op, "retention period cannot be less than 0")
		}
	}
	s.isLeader = p.IsLeader

	return s, nil
}

func (s *Service) StartServices(ctx context.Context, svc *service.Service) {
	// on the leader unit we start additional routines
	if s.isLeader {
		// audit log cleanup routine
		if s.auditLogCleanupPeriod != 0 {
			svc.Go(func() error {
				jimm.NewAuditLogCleanupService(s.jimm.Database, s.auditLogCleanupPeriod).Start(ctx)
				return nil
			})
		}

		// the JWKS rotator
		svc.Go(func() error {
			if err := s.StartJWKSRotator(ctx, time.NewTicker(time.Hour).C, time.Now().UTC().AddDate(0, 3, 0)); err != nil {
				zapctx.Error(ctx, "failed to start JWKS rotator", zap.Error(err))
				return err
			}
			return nil
		})

		// OpenFGA cleanup - cleans up all orphaned tuples
		svc.Go(func() error {
			return s.OpenFGACleanup(ctx, time.NewTicker(6*time.Hour).C)
		})

		// CleanupDyingModels cleanup - cleans up all dying models
		svc.Go(func() error {
			return s.CleanupDyingModels(ctx, time.NewTicker(time.Minute).C)
		})
	}

	// all units periodically update their controller/model metrics
	svc.Go(func() error {
		s.MonitorResources(ctx)
		return nil
	})

	// all units watch for model summaries
	svc.Go(func() error { return s.WatchModelSummaries(ctx) })
}

// setupDischarger set JIMM up as a discharger of 3rd party caveats addressed to it. This is intended
// to enable Juju controllers to check for permissions using a macaroon-based workflow (atm only
// for cross model relations).
func (s *Service) setupDischarger(p Params) (*discharger.MacaroonDischarger, error) {
	cfg := discharger.MacaroonDischargerConfig{
		PublicKey:              p.PublicKey,
		PrivateKey:             p.PrivateKey,
		MacaroonExpiryDuration: p.MacaroonExpiryDuration,
		ControllerUUID:         p.ControllerUUID,
	}
	MacaroonDischarger, err := discharger.NewMacaroonDischarger(cfg, s.jimm.Database, s.jimm.OpenFGAClient)
	if err != nil {
		return nil, errors.E(err)
	}
	return MacaroonDischarger, nil
}

func (s *Service) setupSessionStore(ctx context.Context, sessionSecret []byte, db *db.Database) (*pgstore.PGStore, error) {
	const op = errors.Op("setupSessionStore")

	sqlDb, err := db.DB.DB()
	if err != nil {
		return nil, errors.E(op, err)
	}

	store, err := pgstore.NewPGStoreFromPool(sqlDb, sessionSecret)
	if err != nil {
		zapctx.Error(ctx, "failed to create session store", zap.Error(err))
		return nil, errors.E(op, err, "failed to create session store")
	}

	// Cleanup expired session every 30 minutes
	cleanupQuit, cleanupDone := store.Cleanup(time.Minute * 30)
	s.AddCleanup(func() error {
		store.StopCleanup(cleanupQuit, cleanupDone)
		return nil
	})
	return store, nil
}

func openDB(ctx context.Context, dsn string, logSQL bool) (*gorm.DB, error) {
	zapctx.Info(ctx, "connecting database")

	var dialect gorm.Dialector
	switch {
	case strings.HasPrefix(dsn, "pgx:"):
		dialect = postgres.Open(strings.TrimPrefix(dsn, "pgx:"))
	case strings.HasPrefix(dsn, "postgres:") || strings.HasPrefix(dsn, "postgresql:"):
		dialect = postgres.Open(dsn)
	default:
		return nil, errors.E(errors.CodeServerConfiguration, "unsupported DSN")
	}
	return gorm.Open(dialect, &gorm.Config{
		Logger: &logger.GormLogger{LogSQL: logSQL},
		NowFunc: func() time.Time {
			// This is to set the timestamp precision at the service level.
			return time.Now().Truncate(time.Microsecond)
		},
	})
}

func (s *Service) setupCredentialStore(ctx context.Context, p Params, db *db.Database) (jimmcreds.CredentialStore, error) {
	const op = errors.Op("newSecretStore")

	// Only enable Postgres storage for secrets if explicitly enabled.
	if p.InsecureSecretStorage {
		zapctx.Warn(ctx, "using plaintext postgres for secret storage")
		return db, nil
	}

	vs, err := newVaultStore(ctx, p)
	if err != nil {
		zapctx.Error(ctx, "Vault Store error", zap.Error(err))
		return nil, errors.E(op, err)
	}
	if vs != nil {
		return vs, nil
	}

	return nil, errors.E(op, "jimm cannot start without a credential store")
}

func newVaultStore(ctx context.Context, p Params) (jimmcreds.CredentialStore, error) {
	if p.VaultRoleID == "" || p.VaultRoleSecretID == "" {
		return nil, nil
	}
	zapctx.Info(ctx, "configuring vault client",
		zap.String("VaultAddress", p.VaultAddress),
		zap.String("VaultPath", p.VaultPath),
		zap.String("VaultRoleID", p.VaultRoleID),
	)

	cfg := vaultapi.DefaultConfig()
	if p.VaultAddress != "" {
		cfg.Address = p.VaultAddress
	}

	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &vault.VaultStore{
		Client:       client,
		RoleID:       p.VaultRoleID,
		RoleSecretID: p.VaultRoleSecretID,
		KVPath:       strings.ReplaceAll(p.VaultPath, "/", ""),
	}, nil
}

func newOpenFGAClient(ctx context.Context, p OpenFGAParams) (*openfga.OFGAClient, error) {
	const op = errors.Op("newOpenFGAClient")
	cofgaClient, err := cofga.NewClient(ctx, cofga.OpenFGAParams{
		Scheme:      p.Scheme,
		Host:        p.Host,
		Token:       p.Token,
		Port:        p.Port,
		StoreID:     p.Store,
		AuthModelID: p.AuthModel,
	})
	if err != nil {
		return nil, errors.E(op, err)
	}
	return openfga.NewOpenFGAClient(cofgaClient), nil
}

// ensureControllerAdministrators ensures that listed users have admin access to the JIMM controller.
// This method checks if these users already have administrator access to the JIMM controller,
// otherwise it will add a direct administrator relation between each user and the JIMM
// controller.
func ensureControllerAdministrators(ctx context.Context, client *openfga.OFGAClient, controllerUUID string, admins []string) error {
	controller := names.NewControllerTag(controllerUUID)
	tuples := []openfga.Tuple{}
	for _, username := range admins {
		userTag := names.NewUserTag(username)
		i, err := dbmodel.NewIdentity(userTag.Id())
		if err != nil {
			return errors.E(err)
		}
		user := openfga.NewUser(i, client)
		isAdmin, err := openfga.IsAdministrator(ctx, user, controller)
		if err != nil {
			return errors.E(err)
		}
		if !isAdmin {
			tuples = append(tuples, openfga.Tuple{
				Object:   ofganames.ConvertTag(userTag),
				Relation: ofganames.AdministratorRelation,
				Target:   ofganames.ConvertTag(controller),
			})
		}
	}
	if len(tuples) == 0 {
		return nil
	}
	return client.AddRelation(ctx, tuples...)
}
