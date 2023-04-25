/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/gravitational/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/auth/testauthority"
	"github.com/gravitational/teleport/lib/circleci"
)

func TestAuth_RegisterUsingToken_CircleCI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var (
		validIDToken  = "valid-token"
		validOrg      = "valid-org"
		validProject  = "valid-project"
		validContextA = "valid-context-a"
		validContextB = "valid-context-b"
	)
	// stand up auth server with mocked CircleCI token validator
	var withTokenValidator ServerOption = func(server *Server) error {
		server.circleCITokenValidate = func(
			ctx context.Context, organizationID, token string,
		) (*circleci.IDTokenClaims, error) {
			if organizationID == validOrg && token == validIDToken {
				return &circleci.IDTokenClaims{
					Sub:        "org/valid-org/project/valid-project/user/USER_ID",
					ProjectID:  validProject,
					ContextIDs: []string{validContextA, validContextB},
				}, nil
			}
			return nil, errMockInvalidToken
		}
		return nil
	}
	p, err := newTestPack(ctx, t.TempDir(), withTokenValidator)
	require.NoError(t, err)
	auth := p.a

	// helper for creating RegisterUsingTokenRequest
	sshPrivateKey, sshPublicKey, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)
	tlsPublicKey, err := PrivateKeyToPublicKeyTLS(sshPrivateKey)
	require.NoError(t, err)
	newRequest := func(idToken string) *types.RegisterUsingTokenRequest {
		return &types.RegisterUsingTokenRequest{
			HostID:       "host-id",
			Role:         types.RoleNode,
			IDToken:      idToken,
			PublicTLSKey: tlsPublicKey,
			PublicSSHKey: sshPublicKey,
		}
	}
	provisionTokenSpec := func(spec *types.ProvisionTokenSpecV2CircleCI) types.ProvisionTokenSpecV2 {
		return types.ProvisionTokenSpecV2{
			JoinMethod: types.JoinMethodCircleCI,
			Roles:      types.SystemRoles{types.RoleNode},
			CircleCI:   spec,
		}
	}

	// helpers for error assertions
	allowRulesNotMatched := require.ErrorAssertionFunc(func(t require.TestingT, err error, i ...interface{}) {
		messageMatch := assert.ErrorContains(t, err, "id token claims did not match any allow rules")
		typeMatch := assert.True(t, trace.IsAccessDenied(err))
		require.True(t, messageMatch && typeMatch)
	})
	tokenNotMatched := func(t require.TestingT, err error, i ...interface{}) {
		require.ErrorIs(t, err, errMockInvalidToken)
	}
	tests := []struct {
		name        string
		request     *types.RegisterUsingTokenRequest
		tokenSpec   types.ProvisionTokenSpecV2
		assertError require.ErrorAssertionFunc
	}{
		{
			name:    "matching all",
			request: newRequest(validIDToken),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ProjectID: validProject,
						ContextID: validContextA,
					},
				},
			}),
			assertError: require.NoError,
		},
		{
			name:    "matching second context",
			request: newRequest(validIDToken),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ContextID: validContextB,
					},
				},
			}),
			assertError: require.NoError,
		},
		{
			name:    "invalid org",
			request: newRequest(validIDToken),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: "not-this-org",
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ContextID: validContextB,
					},
				},
			}),
			assertError: tokenNotMatched,
		},
		{
			name:    "invalid IDToken",
			request: newRequest("not-this-token"),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ContextID: validContextB,
					},
				},
			}),
			assertError: tokenNotMatched,
		},
		{
			name:    "missing IDToken in request",
			request: newRequest(""),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ContextID: validContextB,
					},
				},
			}),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.True(t, trace.IsBadParameter(err))
				require.ErrorContains(t, err, "IDToken not provided")
			},
		},
		{
			name:    "invalid context",
			request: newRequest(validIDToken),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ProjectID: validProject,
						ContextID: "not-this-context",
					},
				},
			}),
			assertError: allowRulesNotMatched,
		},
		{
			name:    "invalid project",
			request: newRequest(validIDToken),
			tokenSpec: provisionTokenSpec(&types.ProvisionTokenSpecV2CircleCI{
				OrganizationID: validOrg,
				Allow: []*types.ProvisionTokenSpecV2CircleCI_Rule{
					{
						ProjectID: "invalid-project",
						ContextID: validContextA,
					},
				},
			}),
			assertError: allowRulesNotMatched,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create Token resource for use in test
			token, err := types.NewProvisionTokenFromSpec(
				tt.name, time.Now().Add(time.Minute), tt.tokenSpec,
			)
			require.NoError(t, err)
			require.NoError(t, auth.CreateToken(ctx, token))
			tt.request.Token = token.GetName()

			_, err = auth.RegisterUsingToken(ctx, tt.request)
			tt.assertError(t, err)
		})
	}

}
