package client

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdParse(t *testing.T) {
	structTest := FolderContent{
		Id: float64(397240323),
	}

	require.Equal(t, structTest.ID(), "397240323")
}
