// Copyright 2015 Canonical Ltd.

package jem

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/juju/clock"
	"github.com/juju/juju/api"
	jujuparams "github.com/juju/juju/apiserver/params"
	jujucloud "github.com/juju/juju/cloud"
	"github.com/juju/names/v4"
	"github.com/juju/utils/cache"
	"github.com/juju/version"
	"github.com/rogpeppe/fastuuid"
	"go.uber.org/zap"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/mgo.v2"

	"github.com/CanonicalLtd/jimm/internal/apiconn"
	"github.com/CanonicalLtd/jimm/internal/auth"
	"github.com/CanonicalLtd/jimm/internal/conv"
	"github.com/CanonicalLtd/jimm/internal/jem/jimmdb"
	"github.com/CanonicalLtd/jimm/internal/mgosession"
	"github.com/CanonicalLtd/jimm/internal/mongodoc"
	"github.com/CanonicalLtd/jimm/internal/pubsub"
	"github.com/CanonicalLtd/jimm/internal/zapctx"
	"github.com/CanonicalLtd/jimm/internal/zaputil"
	"github.com/CanonicalLtd/jimm/params"
)

// wallClock provides access to the current time. It is a variable so
// that it can be overridden in tests.
var wallClock clock.Clock = clock.WallClock

// Functions defined as variables so they can be overridden in tests.
var (
	randIntn = rand.Intn

	// ModelSummaryWatcherNotSupportedError is returned by WatchAllModelSummaries if
	// the controller does not support this functionality
	ModelSummaryWatcherNotSupportedError = errgo.New("model summary watcher not supported by the controller")
)

// UsageSenderAuthorizationClient is used to obtain authorization to
// collect and report usage metrics.
type UsageSenderAuthorizationClient interface {
	GetCredentials(ctx context.Context, applicationUser string) ([]byte, error)
}

// Params holds parameters for the NewPool function.
type Params struct {
	// DB holds the mongo database that will be used to
	// store the JEM information.
	DB *mgo.Database

	// SessionPool holds a pool from which session objects are
	// taken to be used in database operations.
	SessionPool *mgosession.Pool

	// ControllerAdmin holds the identity of the user
	// or group that is allowed to create controllers.
	ControllerAdmin params.User

	// UsageSenderAuthorizationClient holds the client to use to  obtain
	// authorization to collect and report usage metrics.
	UsageSenderAuthorizationClient UsageSenderAuthorizationClient

	// Client is used to make the request for usage metrics authorization
	Client *httpbakery.Client

	// PublicCloudMetadata contains the metadata details of all known
	// public clouds.
	PublicCloudMetadata map[string]jujucloud.Cloud

	Pubsub *pubsub.Hub

	// VaultClient is the client for a vault server that is used to store
	// secrets.
	VaultClient *vault.Client

	// VaultPath is the root path in the vault for JIMM's secrets.
	VaultPath string
}

type Pool struct {
	config    Params
	connCache *apiconn.Cache

	// dbName holds the name of the database to use.
	dbName string

	// regionCache caches region information about models
	regionCache *cache.Cache

	// mu guards the fields below it.
	mu sync.Mutex

	// closed holds whether the Pool has been closed.
	closed bool

	// refCount holds the number of JEM instances that
	// currently refer to the pool. The pool is finally
	// closed when all JEM instances are closed and the
	// pool itself has been closed.
	refCount int

	// uuidGenerator is used to generate temporary UUIDs during the
	// creation of models, these UUIDs will be replaced with the ones
	// generated by the controllers themselves.
	uuidGenerator *fastuuid.Generator

	pubsub *pubsub.Hub
}

var APIOpenTimeout = 15 * time.Second

// NewPool represents a pool of possible JEM instances that use the given
// database as a store, and use the given bakery parameters to create the
// bakery.Service.
func NewPool(ctx context.Context, p Params) (*Pool, error) {
	// TODO migrate database
	if p.ControllerAdmin == "" {
		return nil, errgo.Newf("no controller admin group specified")
	}
	if p.SessionPool == nil {
		return nil, errgo.Newf("no session pool provided")
	}
	uuidGen, err := fastuuid.NewGenerator()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	pool := &Pool{
		config:        p,
		dbName:        p.DB.Name,
		connCache:     apiconn.NewCache(apiconn.CacheParams{}),
		regionCache:   cache.New(24 * time.Hour),
		refCount:      1,
		uuidGenerator: uuidGen,
		pubsub:        p.Pubsub,
	}
	jem := pool.JEM(ctx)
	defer jem.Close()
	if err := jem.DB.EnsureIndexes(); err != nil {
		return nil, errgo.Notef(err, "cannot ensure indexes")
	}
	return pool, nil
}

// Close closes the pool. Its resources will be freed
// when the last JEM instance created from the pool has
// been closed.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.decRef()
	p.closed = true
}

func (p *Pool) decRef() {
	// called with p.mu held.
	if p.refCount--; p.refCount == 0 {
		p.connCache.Close()
	}
	if p.refCount < 0 {
		panic("negative reference count")
	}
}

// ClearAPIConnCache clears out the API connection cache.
// This is useful for testing purposes.
func (p *Pool) ClearAPIConnCache() {
	p.connCache.EvictAll()
}

// JEM returns a new JEM instance from the pool, suitable
// for using in short-lived requests. The JEM must be
// closed with the Close method after use.
//
// This method will panic if called after the pool has been
// closed.
func (p *Pool) JEM(ctx context.Context) *JEM {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		panic("JEM call on closed pool")
	}
	p.refCount++
	return &JEM{
		DB:     jimmdb.NewDatabase(ctx, p.config.SessionPool, p.dbName),
		pool:   p,
		pubsub: p.pubsub,
	}
}

// UsageAuthorizationClient returns the UsageSenderAuthorizationClient.
func (p *Pool) UsageAuthorizationClient() UsageSenderAuthorizationClient {
	return p.config.UsageSenderAuthorizationClient
}

type JEM struct {
	// DB holds the mongodb-backed identity store.
	DB *jimmdb.Database

	// pool holds the Pool from which the JEM instance
	// was created.
	pool *Pool

	// closed records whether the JEM instance has
	// been closed.
	closed bool

	usageSenderAuthorizationClient UsageSenderAuthorizationClient

	pubsub *pubsub.Hub
}

// Clone returns an independent copy of the receiver
// that uses a cloned database connection. The
// returned value must be closed after use.
func (j *JEM) Clone() *JEM {
	j.pool.mu.Lock()
	defer j.pool.mu.Unlock()

	j.pool.refCount++
	return &JEM{
		DB:   j.DB.Clone(),
		pool: j.pool,
	}
}

func (j *JEM) ControllerAdmin() params.User {
	return j.pool.config.ControllerAdmin
}

// Close closes the JEM instance. This should be called when
// the JEM instance is finished with.
func (j *JEM) Close() {
	j.pool.mu.Lock()
	defer j.pool.mu.Unlock()
	if j.closed {
		return
	}
	j.closed = true
	j.DB.Session.Close()
	j.DB = nil
	j.pool.decRef()
}

// Pubsub returns jem's pubsub hub.
func (j *JEM) Pubsub() *pubsub.Hub {
	return j.pubsub
}

// ErrAPIConnection is returned by OpenAPI, OpenAPIFromDoc and
// OpenModelAPI when the API connection cannot be made.
//
// Note that it is defined as an ErrorCode so that Database.checkError
// does not treat it as a mongo-connection-broken error.
var ErrAPIConnection params.ErrorCode = "cannot connect to API"

// OpenAPI opens an API connection to the controller with the given path
// and returns it along with the information used to connect. If the
// controller does not exist, the error will have a cause of
// params.ErrNotFound.
//
// If the controller API connection could not be made, the error will
// have a cause of ErrAPIConnection.
//
// The returned connection must be closed when finished with.
func (j *JEM) OpenAPI(ctx context.Context, path params.EntityPath) (*apiconn.Conn, error) {
	ctl := mongodoc.Controller{Path: path}
	if err := j.DB.GetController(ctx, &ctl); err != nil {
		return nil, errgo.NoteMask(err, "cannot get controller", errgo.Is(params.ErrNotFound))
	}
	return j.OpenAPIFromDoc(ctx, &ctl)
}

// OpenAPIFromDoc returns an API connection to the controller held in the
// given document. This can be useful when we want to connect to a
// controller before it's added to the database. Note that a successful
// return from this function does not necessarily mean that the
// credentials or API addresses in the docs actually work, as it's
// possible that there's already a cached connection for the given
// controller.
//
// The returned connection must be closed when finished with.
func (j *JEM) OpenAPIFromDoc(ctx context.Context, ctl *mongodoc.Controller) (*apiconn.Conn, error) {
	return j.pool.connCache.OpenAPI(ctx, ctl.UUID, func() (api.Connection, *api.Info, error) {
		info := apiInfoFromDoc(ctl)
		zapctx.Debug(ctx, "open API", zap.Any("api-info", info))
		conn, err := api.Open(info, apiDialOpts())
		if err != nil {
			return nil, nil, errgo.WithCausef(err, ErrAPIConnection, "")
		}
		return conn, info, nil
	})
}

func apiDialOpts() api.DialOpts {
	return api.DialOpts{
		Timeout:    APIOpenTimeout,
		RetryDelay: 500 * time.Millisecond,
	}
}

func apiInfoFromDoc(ctl *mongodoc.Controller) *api.Info {
	return &api.Info{
		Addrs:    mongodoc.Addresses(ctl.HostPorts),
		CACert:   ctl.CACert,
		Tag:      names.NewUserTag(ctl.AdminUser),
		Password: ctl.AdminPassword,
	}
}

// OpenModelAPI opens an API connection to the model with the given path
// and returns it along with the information used to connect. If the
// model does not exist, the error will have a cause of
// params.ErrNotFound.
//
// If the model API connection could not be made, the error will have a
// cause of ErrAPIConnection.
//
// The returned connection must be closed when finished with.
func (j *JEM) OpenModelAPI(ctx context.Context, path params.EntityPath) (*apiconn.Conn, error) {
	m := mongodoc.Model{Path: path}
	if err := j.DB.GetModel(ctx, &m); err != nil {
		return nil, errgo.NoteMask(err, "cannot get model", errgo.Is(params.ErrNotFound))
	}
	ctl := mongodoc.Controller{Path: m.Controller}
	if err := j.DB.GetController(ctx, &ctl); err != nil {
		return nil, errgo.Notef(err, "cannot get controller")
	}
	return j.openModelAPIFromDocs(ctx, &ctl, &m)
}

// openModelAPIFromDocs returns an API connection to the model held in the
// given documents.
//
// The returned connection must be closed when finished with.
func (j *JEM) openModelAPIFromDocs(ctx context.Context, ctl *mongodoc.Controller, m *mongodoc.Model) (*apiconn.Conn, error) {
	return j.pool.connCache.OpenAPI(ctx, m.UUID, func() (api.Connection, *api.Info, error) {
		info := apiInfoFromDocs(ctl, m)
		zapctx.Debug(ctx, "open API", zap.Any("api-info", info))
		conn, err := api.Open(info, apiDialOpts())
		if err != nil {
			zapctx.Info(ctx, "failed to open connection", zaputil.Error(err), zap.Any("api-info", info))
			return nil, nil, errgo.WithCausef(err, ErrAPIConnection, "")
		}
		return conn, info, nil
	})
}

func apiInfoFromDocs(ctl *mongodoc.Controller, m *mongodoc.Model) *api.Info {
	return &api.Info{
		Addrs:    mongodoc.Addresses(ctl.HostPorts),
		CACert:   ctl.CACert,
		ModelTag: names.NewModelTag(m.UUID),
		Tag:      names.NewUserTag(ctl.AdminUser),
		Password: ctl.AdminPassword,
	}
}

// GetModel retrieves the given model from the database using
// Database.GetModel. It then checks that the given user has the given
// access level on the model. If the model cannot be found then an error
// with a cause of params.ErrNotFound is returned. If the given user
// does not have the correct access level on the model then an error of
// type params.ErrUnauthorized will be returned.
func (j *JEM) GetModel(ctx context.Context, id identchecker.ACLIdentity, access jujuparams.UserAccessPermission, m *mongodoc.Model) error {
	if err := j.DB.GetModel(ctx, m); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	if err := checkModelAccess(ctx, id, access, m); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	if err := j.updateModelContent(ctx, m); err != nil {
		// Log the failure, but return what we have to the caller.
		zapctx.Error(ctx, "cannot update model info", zap.Error(err))
	}
	return nil
}

// check model access checks that that authenticated user has the given
// access level on the given model.
func checkModelAccess(ctx context.Context, id identchecker.ACLIdentity, access jujuparams.UserAccessPermission, m *mongodoc.Model) error {
	// Currently in JAAS the namespace user has full access to the model.
	acl := []string{string(m.Path.User)}
	switch access {
	case jujuparams.ModelReadAccess:
		acl = append(acl, m.ACL.Read...)
		fallthrough
	case jujuparams.ModelWriteAccess:
		acl = append(acl, m.ACL.Write...)
		fallthrough
	case jujuparams.ModelAdminAccess:
		acl = append(acl, m.ACL.Admin...)
	}
	return errgo.Mask(auth.CheckACL(ctx, id, acl), errgo.Is(params.ErrUnauthorized))
}

// updateModelInfo retrieves model parameters missing in the current database
// from the controller.
func (j *JEM) updateModelContent(ctx context.Context, model *mongodoc.Model) error {
	u := new(jimmdb.Update)
	cloud := model.Cloud
	if cloud == "" {
		// The model does not currently store its cloud information so go
		// and fetch it from the model itself. This happens if the model
		// was created with a JIMM version older than 0.9.5.
		conn, err := j.OpenAPI(ctx, model.Controller)
		if err != nil {
			return errgo.Mask(err)
		}
		defer conn.Close()
		info := jujuparams.ModelInfo{UUID: model.UUID}
		if err := conn.ModelInfo(ctx, &info); err != nil {
			return errgo.Mask(err)
		}
		cloudTag, err := names.ParseCloudTag(info.CloudTag)
		if err != nil {
			return errgo.Notef(err, "bad data from controller")
		}
		cloud = params.Cloud(cloudTag.Id())
		credentialTag, err := names.ParseCloudCredentialTag(info.CloudCredentialTag)
		if err != nil {
			return errgo.Notef(err, "bad data from controller")
		}
		owner, err := conv.FromUserTag(credentialTag.Owner())
		if err != nil {
			return errgo.Mask(err, errgo.Is(conv.ErrLocalUser))
		}
		u.Set("cloud", cloud)
		u.Set("credential", mongodoc.CredentialPath{
			Cloud: string(params.Cloud(credentialTag.Cloud().Id())),
			EntityPath: mongodoc.EntityPath{
				User: string(owner),
				Name: credentialTag.Name(),
			},
		})
		u.Set("defaultseries", info.DefaultSeries)
		if info.CloudRegion != "" {
			u.Set("cloudregion", info.CloudRegion)
		}
	}
	if model.ProviderType == "" {
		pt, err := j.DB.ProviderType(ctx, cloud)
		if err != nil {
			return errgo.Mask(err)
		}
		u.Set("providertype", pt)
	}
	if model.ControllerUUID == "" {
		ctl := mongodoc.Controller{Path: model.Controller}
		if err := j.DB.GetController(ctx, &ctl); err != nil {
			return errgo.Mask(err)
		}
		u.Set("controlleruuid", ctl.UUID)
	}
	if u.IsZero() {
		return nil
	}
	return errgo.Mask(j.DB.UpdateModel(ctx, model, u, true))
}

func (j *JEM) possibleControllers(ctx context.Context, id identchecker.ACLIdentity, ctlPath params.EntityPath, cr *mongodoc.CloudRegion) ([]params.EntityPath, error) {
	if ctlPath.Name != "" {
		return []params.EntityPath{ctlPath}, nil
	}
	if err := j.DB.GetCloudRegion(ctx, cr); err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	if err := auth.CheckCanRead(ctx, id, cr); err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	shuffle(len(cr.PrimaryControllers), func(i, j int) {
		cr.PrimaryControllers[i], cr.PrimaryControllers[j] = cr.PrimaryControllers[j], cr.PrimaryControllers[i]
	})
	shuffle(len(cr.SecondaryControllers), func(i, j int) {
		cr.SecondaryControllers[i], cr.SecondaryControllers[j] = cr.SecondaryControllers[j], cr.SecondaryControllers[i]
	})
	return append(cr.PrimaryControllers, cr.SecondaryControllers...), nil
}

// shuffle is used to randomize the order in which possible controllers
// are tried. It is a variable so it can be replaced in tests.
var shuffle func(int, func(int, int)) = rand.Shuffle

// ForEachModel iterates through all models that the authorized user has
// the given access level for. The given function will be called for each
// model. If the given function returns an error iteration will immediately
// stop and the error will be returned with the cause unamasked.
func (j *JEM) ForEachModel(ctx context.Context, id identchecker.ACLIdentity, access jujuparams.UserAccessPermission, f func(*mongodoc.Model) error) error {
	var ferr error
	err := j.DB.ForEachModel(ctx, nil, []string{"path.user", "path.name"}, func(m *mongodoc.Model) error {
		if err := checkModelAccess(ctx, id, access, m); err != nil {
			if errgo.Cause(err) == params.ErrUnauthorized {
				err = nil
			}
			return errgo.Mask(err)
		}
		if err := j.updateModelContent(ctx, m); err != nil {
			// Log the failure, but use what we have.
			zapctx.Error(ctx, "cannot update model info", zap.Error(err))
		}
		if err := f(m); err != nil {
			ferr = err
			return errStop
		}
		return nil
	})
	if errgo.Cause(err) == errStop {
		return errgo.Mask(ferr, errgo.Any)
	}
	return errgo.Mask(err)
}

// EarliestControllerVersion returns the earliest agent version
// that any of the available public controllers is known to be running.
// If there are no available controllers or none of their versions are
// known, it returns the zero version.
func (j *JEM) EarliestControllerVersion(ctx context.Context, id identchecker.ACLIdentity) (version.Number, error) {
	// TOD(rog) cache the result of this for a while, as it changes only rarely
	// and we don't really need to make this extra round trip every
	// time a user connects to the API?
	var v *version.Number
	err := j.DB.ForEachController(ctx, jimmdb.NotExists("unavailablesince"), nil, func(c *mongodoc.Controller) error {
		ctx := zapctx.WithFields(ctx, zap.Stringer("controller", c.Path))
		if err := auth.CheckCanRead(ctx, id, c); err != nil {
			if errgo.Cause(err) != params.ErrUnauthorized {
				zapctx.Warn(ctx, "cannot check read access", zap.Error(err))
			}
			return nil
		}
		zapctx.Debug(ctx, "EarliestControllerVersion", zap.Stringer("version", c.Version))
		if c.Version == nil {
			return nil
		}
		if v == nil || c.Version.Compare(*v) < 0 {
			v = c.Version
		}
		return nil
	})
	if err != nil || v == nil {
		return version.Number{}, errgo.Mask(err)
	}
	return *v, nil
}

// UpdateMachineInfo updates the information associated with a machine.
func (j *JEM) UpdateMachineInfo(ctx context.Context, ctlPath params.EntityPath, info *jujuparams.MachineInfo) error {
	cloud, region, err := j.modelRegion(ctx, ctlPath, info.ModelUUID)
	if errgo.Cause(err) == params.ErrNotFound {
		// If the model isn't found then it is not controlled by
		// JIMM and we aren't interested in it.
		return nil
	}
	if err != nil {
		return errgo.Notef(err, "cannot find region for model %s:%s", ctlPath, info.ModelUUID)
	}
	return errgo.Mask(j.DB.UpdateMachineInfo(ctx, &mongodoc.Machine{
		Controller: ctlPath.String(),
		Cloud:      cloud,
		Region:     region,
		Info:       info,
	}))
}

// UpdateApplicationInfo updates the information associated with an application.
func (j *JEM) UpdateApplicationInfo(ctx context.Context, ctlPath params.EntityPath, info *jujuparams.ApplicationInfo) error {
	cloud, region, err := j.modelRegion(ctx, ctlPath, info.ModelUUID)
	if errgo.Cause(err) == params.ErrNotFound {
		// If the model isn't found then it is not controlled by
		// JIMM and we aren't interested in it.
		return nil
	}
	if err != nil {
		return errgo.Notef(err, "cannot find region for model %s:%s", ctlPath, info.ModelUUID)
	}
	app := &mongodoc.Application{
		Controller: ctlPath.String(),
		Cloud:      cloud,
		Region:     region,
	}
	if info != nil {
		app.Info = &mongodoc.ApplicationInfo{
			ModelUUID:       info.ModelUUID,
			Name:            info.Name,
			Exposed:         info.Exposed,
			CharmURL:        info.CharmURL,
			OwnerTag:        info.OwnerTag,
			Life:            info.Life,
			Subordinate:     info.Subordinate,
			Status:          info.Status,
			WorkloadVersion: info.WorkloadVersion,
		}
	}
	return errgo.Mask(j.DB.UpdateApplicationInfo(ctx, app))
}

// modelRegion determines the cloud and region in which a model is contained.
func (j *JEM) modelRegion(ctx context.Context, ctlPath params.EntityPath, uuid string) (params.Cloud, string, error) {
	type cloudRegion struct {
		cloud  params.Cloud
		region string
	}
	key := fmt.Sprintf("%s %s", ctlPath, uuid)
	r, err := j.pool.regionCache.Get(key, func() (interface{}, error) {
		m := mongodoc.Model{
			UUID:       uuid,
			Controller: ctlPath,
		}
		if err := j.DB.GetModel(ctx, &m); err != nil {
			return nil, errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		return cloudRegion{
			cloud:  m.Cloud,
			region: m.CloudRegion,
		}, nil
	})
	if err != nil {
		return "", "", errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	cr := r.(cloudRegion)
	return cr.cloud, cr.region, nil
}

func (j *JEM) MongoVersion(ctx context.Context) (jujuparams.StringResult, error) {
	result := jujuparams.StringResult{}
	binfo, err := j.pool.config.DB.Session.BuildInfo()
	if err != nil {
		return result, errgo.Mask(err)
	}
	result.Result = binfo.Version
	return result, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// WatchAllModelSummaries starts watching the summary updates from
// the controller.
func (j *JEM) WatchAllModelSummaries(ctx context.Context, ctlPath params.EntityPath) (func() error, error) {
	conn, err := j.OpenAPI(ctx, ctlPath)
	if err != nil {
		return nil, errgo.Mask(err)
	}

	if !conn.SupportsModelSummaryWatcher() {
		return nil, ModelSummaryWatcherNotSupportedError
	}
	id, err := conn.WatchAllModelSummaries(ctx)
	if err != nil {
		errgo.Mask(err, apiconn.IsAPIError)
	}
	watcher := &modelSummaryWatcher{
		conn:    conn,
		id:      id,
		pubsub:  j.pubsub,
		cleanup: conn.Close,
	}
	go watcher.loop(ctx)
	return watcher.stop, nil
}

type modelSummaryWatcher struct {
	conn    *apiconn.Conn
	id      string
	pubsub  *pubsub.Hub
	cleanup func() error
}

func (w *modelSummaryWatcher) next(ctx context.Context) ([]jujuparams.ModelAbstract, error) {
	models, err := w.conn.ModelSummaryWatcherNext(ctx, w.id)
	if err != nil {
		return nil, errgo.Mask(err, apiconn.IsAPIError)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].UUID < models[j].UUID
	})
	return models, nil
}

func (w *modelSummaryWatcher) loop(ctx context.Context) {
	defer func() {
		if err := w.cleanup(); err != nil {
			zapctx.Error(ctx, "cleanup failed", zaputil.Error(err))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		modelSummaries, err := w.next(ctx)
		if err != nil {
			zapctx.Error(ctx, "failed to get next model summary", zaputil.Error(err))
			return
		}
		for _, modelSummary := range modelSummaries {
			w.pubsub.Publish(modelSummary.UUID, modelSummary)
		}
	}
}

func (w *modelSummaryWatcher) stop() error {
	return errgo.Mask(w.conn.ModelSummaryWatcherStop(context.TODO(), w.id), apiconn.IsAPIError)
}
