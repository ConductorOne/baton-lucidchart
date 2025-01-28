package client

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIdParse(t *testing.T) {
	structTest := FolderContent{
		Id: float64(397240323),
	}

	require.Equal(t, structTest.ID(), "397240323")
}
