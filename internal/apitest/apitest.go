// Package apitest provides test fixtures for testing JEM.
package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/canonical/candid/candidtest"
	"github.com/juju/aclstore"
	"github.com/juju/clock/testclock"
	controllerapi "github.com/juju/juju/api/controller"
	"github.com/juju/juju/controller"
	"github.com/juju/simplekv/mgosimplekv"
	"github.com/juju/testing"
	"github.com/juju/testing/httptesting"
	"github.com/rogpeppe/fastuuid"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/mgo.v2"

	external_jem "github.com/CanonicalLtd/jimm"
	"github.com/CanonicalLtd/jimm/internal/jem"
	"github.com/CanonicalLtd/jimm/internal/jemserver"
	"github.com/CanonicalLtd/jimm/internal/jemtest"
	"github.com/CanonicalLtd/jimm/internal/mgosession"
	"github.com/CanonicalLtd/jimm/internal/mongodoc"
	"github.com/CanonicalLtd/jimm/internal/pubsub"
	"github.com/CanonicalLtd/jimm/internal/usagesender"
	"github.com/CanonicalLtd/jimm/jemclient"
	"github.com/CanonicalLtd/jimm/params"
)

// Suite implements a test fixture that contains a JEM server
// and an identity discharging server.
type Suite struct {
	jemtest.JujuConnSuite

	// JEMSrv holds a running instance of JEM.
	JEMSrv *jemserver.Server

	// IDMSrv holds a running instance of the fake identity server.
	IDMSrv *candidtest.Server

	// HTTPSrv holds the running HTTP server that uses IDMSrv.
	HTTPSrv *httptest.Server

	// JEM holds an instance of the JEM store, suitable
	// for invasive testing purposes.
	JEM *jem.JEM

	// Pool holds the pool from which the above JEM was taken.
	Pool *jem.Pool

	// SessionPool holds the session pool used to create new
	// JEM instances.
	SessionPool *mgosession.Pool

	ServerParams              external_jem.ServerParams
	MetricsRegistrationClient *stubMetricsRegistrationClient
	MetricsSpoolPath          string
	Clock                     *testclock.Clock
	ACLStore                  aclstore.ACLStore
	Pubsub                    *pubsub.Hub
}

func (s *Suite) SetUpTest(c *gc.C) {
	ctx := context.Background()

	s.IDMSrv = candidtest.NewServer()
	tpi, err := s.IDMSrv.ThirdPartyInfo(ctx, s.IDMSrv.URL.String())
	c.Assert(err, gc.Equals, nil)

	s.JujuConnSuite.ControllerConfigAttrs = map[string]interface{}{
		controller.IdentityURL:       s.IDMSrv.URL,
		controller.IdentityPublicKey: tpi.PublicKey.String(),
	}
	s.JujuConnSuite.SetUpTest(c)
	conn := s.OpenControllerAPI(c)
	defer conn.Close()
	err = controllerapi.NewClient(conn).GrantController("everyone@external", "login")
	c.Assert(err, gc.Equals, nil)
	s.PatchValue(&jem.APIOpenTimeout, time.Duration(0))
	s.MetricsRegistrationClient = &stubMetricsRegistrationClient{}
	s.PatchValue(&jemserver.NewUsageSenderAuthorizationClient, func(_ string, _ *httpbakery.Client) jem.UsageSenderAuthorizationClient {
		return s.MetricsRegistrationClient
	})
	if s.Clock != nil {
		s.PatchValue(&usagesender.SenderClock, s.Clock)
	}
	s.Pubsub = &pubsub.Hub{
		MaxConcurrency: 10,
	}
	s.ServerParams.Pubsub = s.Pubsub
	s.JEMSrv = s.NewServer(ctx, c, s.Session, s.IDMSrv, s.ServerParams)
	s.HTTPSrv = httptest.NewServer(s.JEMSrv)
	s.SessionPool = mgosession.NewPool(context.TODO(), s.Session, 1)
	s.Pool = s.NewJEMPool(c, s.SessionPool)
	s.JEM = s.Pool.JEM(context.TODO())
	kvstore, err := mgosimplekv.NewStore(s.JEM.DB.C("acls"))
	c.Assert(err, gc.Equals, nil)
	s.ACLStore = aclstore.NewACLStore(kvstore)
}

// NewJEMPool returns a jem.Pool that uses the given
// mgosession.Pool, enabling a custom session pool
// to be used.
func (s *Suite) NewJEMPool(c *gc.C, sessionPool *mgosession.Pool) *jem.Pool {
	session := sessionPool.Session(context.TODO())
	pool, err := jem.NewPool(context.TODO(), jem.Params{
		DB:              session.DB("jem"),
		ControllerAdmin: "controller-admin",
		SessionPool:     sessionPool,
		Pubsub:          s.Pubsub,
	})
	c.Assert(err, gc.Equals, nil)
	session.Close()
	return pool
}

func (s *Suite) TearDownTest(c *gc.C) {
	s.HTTPSrv.Close()
	s.JEMSrv.Close()
	s.IDMSrv.Close()
	s.JEM.Close()
	s.Pool.Close()
	s.SessionPool.Close()
	c.Logf("calling JujuConnSuite.TearDownTest")
	s.JujuConnSuite.TearDownTest(c)
}

// NewClient returns a new JEM client that is configured to talk to
// s.JEMSrv.
func (s *Suite) NewClient(username params.User) *jemclient.Client {
	return jemclient.New(jemclient.NewParams{
		BaseURL: s.HTTPSrv.URL,
		Client:  s.IDMSrv.Client(string(username)),
	})
}

// NewServer returns a new JEM server that uses the given mongo session
// and identity server. If GUILocation is specified in params then that
// will be used instead of the default value.
func (s *Suite) NewServer(ctx context.Context, c *gc.C, session *mgo.Session, idmSrv *candidtest.Server, params external_jem.ServerParams) *jemserver.Server {
	db := session.DB("jem")
	idmSrv.AddUser("agent", candidtest.GroupListGroup)
	config := external_jem.ServerParams{
		DB:                      db,
		ControllerAdmin:         "controller-admin",
		IdentityLocation:        idmSrv.URL.String(),
		CharmstoreLocation:      params.CharmstoreLocation,
		MeteringLocation:        params.MeteringLocation,
		ThirdPartyLocator:       idmSrv,
		AgentUsername:           "agent",
		AgentKey:                idmSrv.UserPublicKey("agent"),
		ControllerUUID:          "914487b5-60e7-42bb-bd63-1adc3fd3a388",
		WebsocketRequestTimeout: 3 * time.Minute,
		UsageSenderURL:          "https://0.1.2.3/omnibus/v2",
		UsageSenderSpoolPath:    s.MetricsSpoolPath,
		Pubsub:                  s.Pubsub,
	}
	if params.UsageSenderURL != "" {
		config.UsageSenderURL = params.UsageSenderURL
	}
	if params.GUILocation != "" {
		config.GUILocation = params.GUILocation
	}
	srv, err := external_jem.NewServer(ctx, config)
	c.Assert(err, gc.Equals, nil)
	return srv.(*jemserver.Server)
}

// AssertAddController adds the specified controller using AddController
// and checks that id succeeds. It returns the controller id.
func (s *Suite) AssertAddController(ctx context.Context, c *gc.C, path params.EntityPath, public bool) params.EntityPath {
	err := s.AddController(ctx, c, path, public)
	c.Assert(err, gc.Equals, nil)
	return path
}

// AddController adds a new controller with the provided path and any
// specified location parameters.
func (s *Suite) AddController(ctx context.Context, c *gc.C, path params.EntityPath, public bool) error {
	// Note that because the cookies acquired in this request don't
	// persist, the discharge macaroon we get won't affect subsequent
	// requests in the caller.
	info := s.APIInfo(c)
	p := &params.AddController{
		EntityPath: path,
		Info: params.ControllerInfo{
			HostPorts:      info.Addrs,
			CACert:         info.CACert,
			User:           info.Tag.Id(),
			Password:       info.Password,
			ControllerUUID: s.ControllerConfig.ControllerUUID(),
			// We only support creating public controllers
			// for now, but we can update them afterwards to
			// tests that require private ones.
			Public: true,
		},
	}
	s.IDMSrv.AddUser(string(path.User), "controller-admin")
	if err := s.NewClient(path.User).AddController(ctx, p); err != nil {
		return err
	}
	return nil
}

var uuidGenerator = fastuuid.MustNewGenerator()

// AssertAddControllerDoc adds a controller document to the database.
// Tests cannot connect to a controller added by this function.
func (s *Suite) AssertAddControllerDoc(c *gc.C, cnt *mongodoc.Controller, primaryCloudRegion *mongodoc.CloudRegion) *mongodoc.Controller {
	if cnt.UUID == "" {
		cnt.UUID = fmt.Sprintf("%x", uuidGenerator.Next())
	}
	err := s.JEM.DB.InsertController(context.Background(), cnt)
	c.Assert(err, gc.Equals, nil)
	if primaryCloudRegion != nil {
		err = s.JEM.DB.UpdateCloudRegions(context.Background(), []mongodoc.CloudRegion{*primaryCloudRegion})
		c.Assert(err, gc.Equals, nil)
	}
	return cnt
}

const dummySSHKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDOjaOjVRHchF2RFCKQdgBqrIA5nOoqSprLK47l2th5I675jw+QYMIihXQaITss3hjrh3+5ITyBO41PS5rHLNGtlYUHX78p9CHNZsJqHl/z1Ub1tuMe+/5SY2MkDYzgfPtQtVsLasAIiht/5g78AMMXH3HeCKb9V9cP6/lPPq6mCMvg8TDLrPp/P2vlyukAsJYUvVgoaPDUBpedHbkMj07pDJqe4D7c0yEJ8hQo/6nS+3bh9Q1NvmVNsB1pbtk3RKONIiTAXYcjclmOljxxJnl1O50F5sOIi38vyl7Q63f6a3bXMvJEf1lnPNJKAxspIfEu8gRasny3FEsbHfrxEwVj rog@rog-x220"

var dummyModelConfig = map[string]interface{}{
	"authorized-keys": dummySSHKey,
	"controller":      true,
}

// CreateModel creates a new model with the specified path on the
// specified controller, using the specified credentialss. It returns the
// new model's path, user and uuid.
func (s *Suite) CreateModel(ctx context.Context, c *gc.C, path, ctlPath params.EntityPath, cred params.CredentialName) (modelPath params.EntityPath, uuid string) {
	// Note that because the cookies acquired in this request don't
	// persist, the discharge macaroon we get won't affect subsequent
	// requests in the caller.
	resp, err := s.NewClient(path.User).NewModel(ctx, &params.NewModel{
		User: path.User,
		Info: params.NewModelInfo{
			Name:       path.Name,
			Controller: &ctlPath,
			Credential: params.CredentialPath{
				Cloud: "dummy",
				User:  path.User,
				Name:  cred,
			},
			Location: map[string]string{
				"cloud": "dummy",
			},
			Config: dummyModelConfig,
		},
	})
	c.Assert(err, gc.Equals, nil)

	s.MetricsRegistrationClient.CheckCalls(c, []testing.StubCall{{
		FuncName: "AuthorizeReseller",
		Args: []interface{}{
			string(path.User),
		},
	}})
	s.MetricsRegistrationClient.ResetCalls()

	return resp.Path, resp.UUID
}

func (s *Suite) AssertUpdateCredential(ctx context.Context, c *gc.C, user params.User, cloud params.Cloud, name params.CredentialName, authType string) params.CredentialName {
	err := s.UpdateCredential(ctx, user, cloud, name, authType)
	c.Assert(err, gc.Equals, nil)
	return name
}

// UpdateCredential sets a  credential with the provided path and authType.
func (s *Suite) UpdateCredential(ctx context.Context, user params.User, cloud params.Cloud, name params.CredentialName, authType string) error {
	// Note that because the cookies acquired in this request don't
	// persist, the discharge macaroon we get won't affect subsequent
	// requests in the caller.
	p := &params.UpdateCredential{
		CredentialPath: params.CredentialPath{
			Cloud: cloud,
			User:  user,
			Name:  name,
		},
		Credential: params.Credential{
			AuthType: authType,
		},
	}
	return s.NewClient(user).UpdateCredential(ctx, p)
}

// Do returns a Do function appropriate for using in httptesting.AssertJSONCall.Do
// that makes its HTTP request acting as the given client.
// If client is nil, it uses httpbakery.NewClient instead.
//
// This can be used to cause the HTTP request to act as an
// arbitrary user.
func Do(client *httpbakery.Client) func(*http.Request) (*http.Response, error) {
	if client == nil {
		client = httpbakery.NewClient()
	}
	return client.Do
}

// AnyBody is a convenience value that can be used in
// httptesting.AssertJSONCall.ExpectBody to cause
// AssertJSONCall to ignore the contents of the response body.
var AnyBody = httptesting.BodyAsserter(func(*gc.C, json.RawMessage) {})

type stubMetricsRegistrationClient struct {
	testing.Stub
}

func (c *stubMetricsRegistrationClient) GetCredentials(_ context.Context, applicationUser string) ([]byte, error) {
	c.MethodCall(c, "AuthorizeReseller", applicationUser)
	return []byte("secret credentials"), c.NextErr()
}
