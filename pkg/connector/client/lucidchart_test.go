package client

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractPageToken(t *testing.T) {
	cases := []struct {
		Name          string
		Link          string
		ExpectedToken string
	}{
		{
			Name:          "empty link",
			Link:          "",
			ExpectedToken: "",
		},
		{
			Name:          "link with token",
			Link:          "<https://api.lucid.co/users?pageSize=1&pageToken=eyJvIjoiMSJ9>; rel=\"next\"",
			ExpectedToken: "eyJvIjoiMSJ9",
		},
	}

	for _, s := range cases {
		t.Run(s.Name, func(t *testing.T) {
			token, err := extractPageToken(s.Link)
			require.NoError(t, err)
			require.Equal(t, s.ExpectedToken, token)
		})
	}
}
