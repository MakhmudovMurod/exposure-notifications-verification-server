// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testsuite

import (
	"context"
	"crypto/sha1"
	"io/ioutil"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/exposure-notifications-server/pkg/keys"
	"github.com/google/exposure-notifications-server/pkg/observability"
	"github.com/google/exposure-notifications-verification-server/internal/project"
	"github.com/google/exposure-notifications-verification-server/pkg/cache"
	"github.com/google/exposure-notifications-verification-server/pkg/config"
	"github.com/google/exposure-notifications-verification-server/pkg/controller"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/certapi"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/codes"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/issueapi"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/middleware"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/verifyapi"
	"github.com/google/exposure-notifications-verification-server/pkg/database"
	"github.com/google/exposure-notifications-verification-server/pkg/ratelimit"
	"github.com/google/exposure-notifications-verification-server/pkg/render"
	"github.com/gorilla/mux"
	"github.com/mikehelmick/go-chaff"
)

const (
	VerificationTokenDuration = time.Second * 2
	APIKeyCacheDuration       = time.Second * 2

	APISrvPort   = "8080"
	AdminSrvPort = "8081"
)

// IntegrationTestConfig represents configurations to run server integration tests.
type IntegrationTestConfig struct {
	Observability *observability.Config
	DBConfig      *database.Config

	APISrvConfig      config.APIServerConfig
	AdminAPISrvConfig config.AdminAPIServerConfig
}

func NewIntegrationTestConfig(ctx context.Context, tb testing.TB) (*IntegrationTestConfig, *database.Database) {
	testDatabaseInstance, err := database.NewTestInstance()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := testDatabaseInstance.Close(); err != nil {
			tb.Fatal(err)
		}
	})

	db, dbConfig := testDatabaseInstance.NewDatabase(tb, nil)

	obConfig := &observability.Config{ExporterType: "NOOP"}
	cacheConfig := cache.Config{Type: "IN_MEMORY"}
	rlConfig := ratelimit.Config{Type: "NOOP"}

	tmpdir, err := ioutil.TempDir("", "")
	if err != nil {
		tb.Fatal(err)
	}
	keyConfig := keys.Config{
		KeyManagerType: "FILESYSTEM",
		FilesystemRoot: tmpdir,
	}

	kms, err := keys.KeyManagerFor(ctx, &keyConfig)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := os.RemoveAll(tmpdir); err != nil {
			tb.Fatal(err)
		}
	})

	parent := keys.TestSigningKey(tb, kms)
	skm, ok := kms.(keys.SigningKeyManager)
	if !ok {
		tb.Fatal("KMS doesn't implement interface SigningKeyManager")
	}
	keyID, err := skm.CreateKeyVersion(ctx, parent)
	if err != nil {
		tb.Fatal(err)
	}

	tsConfig := config.TokenSigningConfig{
		Keys:               keyConfig,
		TokenSigningKeys:   []string{keyID},
		TokenSigningKeyIDs: []string{"v1"},
		TokenIssuer:        "diagnosis-verification-example",
	}

	csConfig := config.CertificateSigningConfig{
		Keys:                    keyConfig,
		PublicKeyCacheDuration:  15 * time.Minute,
		SignerCacheDuration:     time.Minute,
		CertificateSigningKey:   keyID,
		CertificateSigningKeyID: "v1",
		CertificateIssuer:       "diagnosis-verification-example",
		CertificateAudience:     "exposure-notifications-server",
		CertificateDuration:     15 * time.Minute,
	}

	cfg := IntegrationTestConfig{
		Observability: obConfig,
		DBConfig:      dbConfig,
		APISrvConfig: config.APIServerConfig{
			Database:                  *dbConfig,
			Observability:             *obConfig,
			Cache:                     cacheConfig,
			DevMode:                   true,
			Port:                      APISrvPort,
			APIKeyCacheDuration:       APIKeyCacheDuration,
			VerificationTokenDuration: VerificationTokenDuration,
			TokenSigning:              tsConfig,
			CertificateSigning:        csConfig,
			RateLimit:                 rlConfig,
		},
		AdminAPISrvConfig: config.AdminAPIServerConfig{
			Database:            *dbConfig,
			Observability:       *obConfig,
			Cache:               cacheConfig,
			DevMode:             true,
			RateLimit:           rlConfig,
			Port:                AdminSrvPort,
			APIKeyCacheDuration: APIKeyCacheDuration,
			CollisionRetryCount: 6,
			AllowedSymptomAge:   time.Hour * 336,
		},
	}

	return &cfg, db
}

// IntegrationSuite contains the integration test configs and other useful data.
type IntegrationSuite struct {
	cfg *IntegrationTestConfig

	DB    *database.Database
	Realm *database.Realm

	adminKey, deviceKey string
}

// NewIntegrationSuite creates a IntegrationSuite for integration tests.
func NewIntegrationSuite(tb testing.TB, ctx context.Context) *IntegrationSuite {
	tb.Helper()

	cfg, db := NewIntegrationTestConfig(ctx, tb)
	if err := db.Open(ctx); err != nil {
		tb.Fatalf("failed to connect to database: %v", err)
	}
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Errorf("failed to close db: %v", err)
		}
	})
	randomStr, err := project.RandomHexString(6)
	if err != nil {
		tb.Fatalf("failed to generate random string: %v", err)
	}
	realmName := realmNamePrefix + randomStr
	// Create or reuse the existing realm
	realm, err := db.FindRealmByName(realmName)
	if err != nil {
		if !database.IsNotFound(err) {
			tb.Fatalf("error when finding the realm %q: %v", realmName, err)
		}
		realm = database.NewRealmWithDefaults(realmName)
		realm.RegionCode = realmRegionCode
		realm.AllowBulkUpload = true
		if err := db.SaveRealm(realm, database.SystemTest); err != nil {
			tb.Fatalf("failed to create realm %+v: %v: %v", realm, err, realm.ErrorMessages())
		}
	}

	// Create new API keys
	suffix, err := project.RandomHexString(6)
	if err != nil {
		tb.Fatalf("failed to create suffix string for API keys: %v", err)
	}

	adminKey, err := realm.CreateAuthorizedApp(db, &database.AuthorizedApp{
		Name:       adminKeyName + suffix,
		APIKeyType: database.APIKeyTypeAdmin,
	}, database.SystemTest)
	if err != nil {
		tb.Fatalf("error trying to create a new Admin API Key: %v", err)
	}

	deviceKey, err := realm.CreateAuthorizedApp(db, &database.AuthorizedApp{
		Name:       deviceKeyName + suffix,
		APIKeyType: database.APIKeyTypeDevice,
	}, database.SystemTest)
	if err != nil {
		tb.Fatalf("error trying to create a new Device API Key: %v", err)
	}

	return &IntegrationSuite{
		cfg:       cfg,
		DB:        db,
		Realm:     realm,
		adminKey:  adminKey,
		deviceKey: deviceKey,
	}
}

// NewAdminAPIClient runs an Admin API Server and returns a corresponding client.
func (s *IntegrationSuite) NewAdminAPIClient(ctx context.Context, tb testing.TB) (*AdminClient, error) {
	srv := s.newAdminAPIServer(ctx, tb)
	return &AdminClient{
		urlBase: srv.URL,
		client:  srv.Client(),
		key:     s.adminKey,
	}, nil
}

// NewAPIClient runs an API Server and returns a corresponding client.
func (s *IntegrationSuite) NewAPIClient(ctx context.Context, tb testing.TB) (*APIClient, error) {
	srv := s.newAPIServer(ctx, tb)
	return &APIClient{
		urlBase: srv.URL,
		client:  srv.Client(),
		key:     s.deviceKey,
	}, nil
}

func (s *IntegrationSuite) newAdminAPIServer(ctx context.Context, tb testing.TB) *httptest.Server {
	// Create the router
	adminRouter := mux.NewRouter()
	// Install common security headers
	adminRouter.Use(middleware.SecureHeaders(s.cfg.AdminAPISrvConfig.DevMode, "json"))

	// Enable debug headers
	processDebug := middleware.ProcessDebug()
	adminRouter.Use(processDebug)

	// Create the renderer
	h, err := render.New(ctx, "", s.cfg.APISrvConfig.DevMode)
	if err != nil {
		tb.Fatalf("failed to create the renderer %v", err)
	}

	// Setup cacher
	cacher, err := cache.CacherFor(ctx, &s.cfg.APISrvConfig.Cache, cache.HMACKeyFunc(sha1.New, s.cfg.APISrvConfig.Cache.HMACKey))
	if err != nil {
		tb.Fatalf("failed to create cacher: %v", err)
	}
	tb.Cleanup(func() {
		if err := cacher.Close(); err != nil {
			tb.Fatalf("failed to close cacher: %v", err)
		}
	})

	// Create LimitStore
	limiterStore, err := ratelimit.RateLimiterFor(ctx, &s.cfg.AdminAPISrvConfig.RateLimit)
	if err != nil {
		tb.Fatalf("failed to create the limit store %v", err)
	}

	adminRouter.Handle("/health", controller.HandleHealthz(ctx, &s.cfg.AdminAPISrvConfig.Database, h)).Methods("GET")

	{
		sub := adminRouter.PathPrefix("/api").Subrouter()

		// Setup API auth
		requireAPIKey := middleware.RequireAPIKey(cacher, s.DB, h, []database.APIKeyType{
			database.APIKeyTypeAdmin,
		})
		// Install the APIKey Auth Middleware
		sub.Use(requireAPIKey)

		issueapiController := issueapi.New(&s.cfg.AdminAPISrvConfig, s.DB, limiterStore, h)
		sub.Handle("/issue", issueapiController.HandleIssueAPI()).Methods("POST")
		sub.Handle("/batch-issue", issueapiController.HandleBatchIssueAPI()).Methods("POST")

		codesController := codes.NewAPI(ctx, &s.cfg.AdminAPISrvConfig, s.DB, h)
		sub.Handle("/checkcodestatus", codesController.HandleCheckCodeStatus()).Methods("POST")
		sub.Handle("/expirecode", codesController.HandleExpireAPI()).Methods("POST")
	}

	srv := httptest.NewServer(adminRouter)
	tb.Cleanup(func() {
		srv.Close()
	})
	return srv
}

func (s *IntegrationSuite) newAPIServer(ctx context.Context, tb testing.TB) *httptest.Server {
	// Create the renderer
	h, err := render.New(ctx, "", s.cfg.APISrvConfig.DevMode)
	if err != nil {
		tb.Fatalf("failed to create the renderer %v", err)
	}

	// Setup cacher
	cacher, err := cache.CacherFor(ctx, &s.cfg.APISrvConfig.Cache, cache.HMACKeyFunc(sha1.New, s.cfg.APISrvConfig.Cache.HMACKey))
	if err != nil {
		tb.Fatalf("failed to create cacher: %v", err)
	}
	tb.Cleanup(func() {
		if err := cacher.Close(); err != nil {
			tb.Fatalf("failed to close cacher: %v", err)
		}
	})

	// Setup signers
	tokenSigner, err := keys.KeyManagerFor(ctx, &s.cfg.APISrvConfig.TokenSigning.Keys)
	if err != nil {
		tb.Fatalf("failed to create token key manager: %v", err)
	}
	certificateSigner, err := keys.KeyManagerFor(ctx, &s.cfg.APISrvConfig.CertificateSigning.Keys)
	if err != nil {
		tb.Fatalf("failed to create certificate key manager: %v", err)
	}

	apiRouter := mux.NewRouter()
	// Install common security headers
	apiRouter.Use(middleware.SecureHeaders(s.cfg.APISrvConfig.DevMode, "json"))

	apiRouter.Handle("/health", controller.HandleHealthz(ctx, &s.cfg.APISrvConfig.Database, h)).Methods("GET")

	{
		sub := apiRouter.PathPrefix("/api").Subrouter()

		// Setup API auth
		requireAPIKey := middleware.RequireAPIKey(cacher, s.DB, h, []database.APIKeyType{
			database.APIKeyTypeDevice,
		})
		// Install the APIKey Auth Middleware
		sub.Use(requireAPIKey)

		verifyChaff := chaff.New()
		defer verifyChaff.Close()
		verifyapiController, err := verifyapi.New(ctx, &s.cfg.APISrvConfig, s.DB, h, tokenSigner)
		if err != nil {
			tb.Fatalf("failed to create verify api controller: %v", err)
		}
		sub.Handle("/verify", verifyapiController.HandleVerify()).Methods("POST")

		certChaff := chaff.New()
		defer certChaff.Close()
		certapiController, err := certapi.New(ctx, &s.cfg.APISrvConfig, s.DB, cacher, certificateSigner, h)
		if err != nil {
			tb.Fatalf("failed to create cert api controller: %v", err)
		}
		sub.Handle("/certificate", certapiController.HandleCertificate()).Methods("POST")
	}

	srv := httptest.NewServer(apiRouter)
	tb.Cleanup(func() {
		srv.Close()
	})
	return srv
}
