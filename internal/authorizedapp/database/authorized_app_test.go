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

package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/exposure-notifications-server/internal/authorizedapp/model"
	coredb "github.com/google/exposure-notifications-server/internal/database"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type testSecretManager struct {
	values map[string]string
}

func (s *testSecretManager) GetSecretValue(ctx context.Context, name string) (string, error) {
	v, ok := s.values[name]
	if !ok {
		return "", fmt.Errorf("missing %q", name)
	}
	return v, nil
}

var testDB *coredb.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	if os.Getenv("DB_USER") != "" {
		var err error
		testDB, err = coredb.CreateTestDB(ctx)
		if err != nil {
			log.Fatalf("creating test DB: %v", err)
		}
	}
	os.Exit(m.Run())
}
func TestGetAuthorizedApp(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	defer coredb.ResetTestDB(t, testDB)
	ctx := context.Background()

	// Create private key for parsing later
	p8PrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	derKey, err := x509.MarshalPKCS8PrivateKey(p8PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: derKey,
	})

	sm := &testSecretManager{
		values: map[string]string{
			"private_key": string(pemBytes),
		},
	}

	cases := []struct {
		name string
		sql  string
		args []interface{}
		exp  *model.AuthorizedApp
		err  bool
	}{
		{
			name: "bare",
			sql: `
				INSERT INTO AuthorizedApp (app_package_name, platform, allowed_regions)
				VALUES ($1, $2, $3)
			`,

			args: []interface{}{"myapp", "android", []string{"US"}},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "android",
				AllowedRegions:           map[string]struct{}{"US": {}},
				SafetyNetBasicIntegrity:  true,
				SafetyNetCTSProfileMatch: true,
			},
		},
		{
			name: "all_regions",
			sql: `
				INSERT INTO AuthorizedApp (app_package_name, platform, allowed_regions)
				VALUES ($1, $2, $3)
			`,
			args: []interface{}{"myapp", "android", []string{}},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "android",
				AllowedRegions:           map[string]struct{}{},
				SafetyNetBasicIntegrity:  true,
				SafetyNetCTSProfileMatch: true,
			},
		},
		{
			name: "safetynet_fileds",
			sql: `
				INSERT INTO AuthorizedApp (
					app_package_name, platform, allowed_regions,
					safetynet_apk_digest, safetynet_cts_profile_match, safetynet_basic_integrity
				)
				VALUES (
					$1, $2, $3,
					$4, $5, $6
				)
			`,
			args: []interface{}{
				"myapp", "android", []string{},
				[]string{"092fcfb", "252f10c"}, false, false,
			},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "android",
				AllowedRegions:           map[string]struct{}{},
				SafetyNetApkDigestSHA256: []string{"092fcfb", "252f10c"},
				SafetyNetBasicIntegrity:  false,
				SafetyNetCTSProfileMatch: false,
			},
		},

		{
			name: "safetynet_past_seconds",
			sql: `
				INSERT INTO AuthorizedApp (
					app_package_name, platform, allowed_regions,
					safetynet_past_seconds
				) VALUES ($1, $2, $3, $4)
			`,
			args: []interface{}{"myapp", "android", []string{"US"}, 1800},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "android",
				AllowedRegions:           map[string]struct{}{"US": {}},
				SafetyNetBasicIntegrity:  true,
				SafetyNetCTSProfileMatch: true,
				SafetyNetPastTime:        30 * time.Minute,
			},
		},
		{
			name: "safetynet_future_seconds",
			sql: `
				INSERT INTO AuthorizedApp (
					app_package_name, platform, allowed_regions,
					safetynet_future_seconds
				) VALUES ($1, $2, $3, $4)
			`,
			args: []interface{}{"myapp", "android", []string{"US"}, 1800},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "android",
				AllowedRegions:           map[string]struct{}{"US": {}},
				SafetyNetBasicIntegrity:  true,
				SafetyNetCTSProfileMatch: true,
				SafetyNetFutureTime:      30 * time.Minute,
			},
		},
		{
			name: "devicecheck",
			sql: `
				INSERT INTO AuthorizedApp (
					app_package_name, platform, allowed_regions,
					devicecheck_team_id, devicecheck_key_id, devicecheck_private_key_secret
				) VALUES ($1, $2, $3, $4, $5, $6)
			`,
			args: []interface{}{"myapp", "ios", []string{"US"}, "ABCD1234", "DEFG5678", "private_key"},
			exp: &model.AuthorizedApp{
				AppPackageName:           "myapp",
				Platform:                 "ios",
				AllowedRegions:           map[string]struct{}{"US": {}},
				SafetyNetCTSProfileMatch: true,
				SafetyNetBasicIntegrity:  true,
				DeviceCheckTeamID:        "ABCD1234",
				DeviceCheckKeyID:         "DEFG5678",
				DeviceCheckPrivateKey:    p8PrivateKey,
			},
		},
		{
			name: "not_found",
			sql:  "",
			args: nil,
			exp:  nil,
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.name, func(t *testing.T) {
			// Acquire a connection
			conn, err := testDB.Pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			defer coredb.ResetTestDB(t, testDB)

			// Insert the data
			if _, err := conn.Exec(ctx, c.sql, c.args...); err != nil {
				t.Fatal(err)
			}

			config, err := NewAuthorizedAppDB(testDB).GetAuthorizedApp(ctx, sm, "myapp")
			if (err != nil) != c.err {
				t.Fatal(err)
			}

			// Compare, ignoring the private key part
			opts := cmpopts.IgnoreTypes(new(ecdsa.PrivateKey))
			if diff := cmp.Diff(config, c.exp, opts); diff != "" {
				t.Errorf("mismatch (-want, +got):\n%s", diff)
			}
		})
	}
}
