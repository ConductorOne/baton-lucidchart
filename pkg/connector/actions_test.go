package connector

import (
	"context"
	"testing"

	"github.com/conductorone/baton-lucidchart/pkg/connector/client"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestUpdateUserHandler_ScimNotConfigured_ReturnsUnimplemented(t *testing.T) {
	t.Parallel()

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-test-token"}) //nolint:gosec // G101: test token literal
	lc, err := client.NewLucidchartClient(context.Background(), "api-key", ts, "http://localhost", "", "")
	require.NoError(t, err)

	c := &Connector{client: lc}

	args, err := structpb.NewStruct(map[string]any{"user_id": "user-123"})
	require.NoError(t, err)

	_, _, handlerErr := c.updateUserHandler(context.Background(), args)
	require.Error(t, handlerErr)
	st, ok := status.FromError(handlerErr)
	require.True(t, ok, "error must be a gRPC status error")
	require.Equal(t, codes.Unimplemented, st.Code())
}
