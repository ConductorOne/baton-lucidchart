package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestExtractPageToken(t *testing.T) {
	cases := []struct {
		Name          string
		Link          string
		ExpectedToken string
		ExpectedError bool
	}{
		{
			Name:          "empty link",
			Link:          "",
			ExpectedToken: "",
			ExpectedError: true,
		},
		{ //nolint:gosec // G101: test data containing fake pageToken, not a real credential
			Name:          "link with token",
			Link:          "<https://api.lucid.co/users?pageSize=1&pageToken=eyJvIjoiMSJ9>; rel=\"next\"",
			ExpectedToken: "eyJvIjoiMSJ9",
			ExpectedError: false,
		},
	}

	for _, s := range cases {
		t.Run(s.Name, func(t *testing.T) {
			token, err := extractPageToken(s.Link)
			if s.ExpectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, s.ExpectedToken, token)
		})
	}
}

// scim-base-url is customer-settable, so this check is the only thing standing
// between a typo and either Enterprise SCIM tokens on a cleartext wire or a
// confusing failure at request time. A rejection disables the SCIM surface; it
// deliberately does not fail construction, which would take sync down with it.
func TestNewLucidchartClientValidatesScimBaseURL(t *testing.T) {
	cases := []struct {
		Name                string
		ScimBaseURL         string
		ExpectedError       bool
		ExpectedScimBaseURL string
	}{
		{
			Name:                "empty falls back to the default",
			ScimBaseURL:         "",
			ExpectedScimBaseURL: string(LucidScimUrl),
		},
		{
			Name:                "https is accepted",
			ScimBaseURL:         "https://acme.users.lucidgov.app/scim/v2",
			ExpectedScimBaseURL: "https://acme.users.lucidgov.app/scim/v2",
		},
		{
			// Lucid's published commercial SCIM URL.
			Name:                "the standard lucid.app host is accepted",
			ScimBaseURL:         "https://users.lucid.app/scim/v2",
			ExpectedScimBaseURL: "https://users.lucid.app/scim/v2",
		},
		{
			// FedRAMP/GovSuite tenants get an account-specific hostname that Lucid
			// generates and never publishes, so the host is not constrained at all:
			// any allowlist would be a guess that rejects some real tenant's URL.
			Name:                "an arbitrary https host is accepted",
			ScimBaseURL:         "https://scim.acme-gov.example.com/scim/v2",
			ExpectedScimBaseURL: "https://scim.acme-gov.example.com/scim/v2",
		},
		{
			// Cleartext would put the Enterprise SCIM bearer token on the wire.
			Name:          "http is rejected",
			ScimBaseURL:   "http://users.lucid.app/scim/v2",
			ExpectedError: true,
		},
		{
			// url.Parse accepts this as a relative path, so only an explicit
			// scheme check catches it.
			Name:          "schemeless is rejected",
			ScimBaseURL:   "users.lucid.app/scim/v2",
			ExpectedError: true,
		},
		{
			Name:          "scheme without a host is rejected",
			ScimBaseURL:   "https:///scim/v2",
			ExpectedError: true,
		},
		{
			// Loopback cleartext never leaves the machine; this is what lets the
			// SCIM tests run the real constructor against httptest.
			Name:                "http loopback is allowed",
			ScimBaseURL:         "http://127.0.0.1:8080/scim/v2",
			ExpectedScimBaseURL: "http://127.0.0.1:8080/scim/v2",
		},
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: static token literal for tests, not a real credential

	for _, s := range cases {
		t.Run(s.Name, func(t *testing.T) {
			c, err := NewLucidchartClient(context.Background(), LucidchartConfig{ //nolint:gosec // G101: static token literal for tests, not a real credential
				APIKey:      "api-key",
				TokenSource: ts,
				ScimToken:   "scim-test-token",
				ScimBaseURL: s.ScimBaseURL,
			})
			// Construction always succeeds: a rejected SCIM URL disables SCIM,
			// it does not take the connector (and with it sync) down.
			require.NoError(t, err)
			require.NotNil(t, c)

			if s.ExpectedError {
				require.Error(t, c.scimBaseURLErr)
				// The message must name both the offending value and the flag.
				require.Contains(t, c.scimBaseURLErr.Error(), s.ScimBaseURL)
				require.Contains(t, c.scimBaseURLErr.Error(), "scim-base-url")

				// No SCIM request may be built while the URL is rejected — that is
				// what keeps the bearer token away from the host it named.
				req, reqErr := c.newScimRequestWithToken(context.Background(), "scim-test-token", http.MethodGet, "/Users/7", nil)
				require.Error(t, reqErr)
				require.Nil(t, req)

				return
			}

			require.NoError(t, c.scimBaseURLErr)
			require.Equal(t, s.ExpectedScimBaseURL, c.scimBaseURL)
		})
	}
}
