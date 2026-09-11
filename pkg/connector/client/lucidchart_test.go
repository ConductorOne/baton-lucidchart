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
		Name        string
		ScimBaseURL string
		// ExpectedErrorNames are substrings the rejection message must carry, on
		// top of the value and the flag name every rejection names.
		ExpectedErrorNames  []string
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
			// FedRAMP/GovSuite tenants get an account-specific hostname, so the
			// check has to allow arbitrary labels under the Lucid domain.
			Name:                "a FedRAMP host under lucidgov.app is accepted",
			ScimBaseURL:         "https://scim.acme-gov.lucidgov.app/scim/v2",
			ExpectedScimBaseURL: "https://scim.acme-gov.lucidgov.app/scim/v2",
		},
		{
			// https proves only that nobody is reading in transit, not who is
			// answering — and both Enterprise SCIM tokens go wherever this points.
			Name:               "an unrelated https host is rejected",
			ScimBaseURL:        "https://scim.example.com/scim/v2",
			ExpectedError:      true,
			ExpectedErrorNames: []string{"lucid.app", "lucidgov.app", "lucid.co"},
		},
		{
			// A substring match would accept this; the domain check is anchored
			// on a label boundary precisely so that it does not.
			Name:          "a lookalike host that merely contains a Lucid domain is rejected",
			ScimBaseURL:   "https://evil-lucid.app.attacker.com/scim/v2",
			ExpectedError: true,
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

				// A rejected host is only actionable if the message says which
				// domains would have been accepted.
				for _, name := range s.ExpectedErrorNames {
					require.Contains(t, err.Error(), name)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, s.ExpectedScimBaseURL, c.scimBaseURL)
		})
	}
}
