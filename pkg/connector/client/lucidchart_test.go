package client

import (
	"context"
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

// scim-base-url is customer-settable, so the construction-time check is the only
// thing standing between a typo and either Enterprise SCIM tokens on a cleartext
// wire or a confusing failure at request time.
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
			c, err := NewLucidchartClient(context.Background(), "api-key", ts, "", "scim-test-token", s.ScimBaseURL, "")
			if s.ExpectedError {
				require.Error(t, err)
				require.Nil(t, c)
				// The message must name both the offending value and the flag.
				require.Contains(t, err.Error(), s.ScimBaseURL)
				require.Contains(t, err.Error(), "scim-base-url")
				return
			}

			require.NoError(t, err)
			require.Equal(t, s.ExpectedScimBaseURL, c.scimBaseURL)
		})
	}
}
