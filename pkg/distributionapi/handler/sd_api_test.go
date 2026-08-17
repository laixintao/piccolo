package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateHolder(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateHolder("10.0.0.12:5001"))
	require.Error(t, validateHolder("10.0.0.12"))
	require.Error(t, validateHolder("hostname:5001"))
	require.Error(t, validateHolder("[2001:db8::1]:5001"))
}

func TestClosestFirst(t *testing.T) {
	t.Parallel()

	holders := []string{"192.168.1.10:5001", "10.0.2.10:5001", "10.0.1.10:5001"}
	got, err := closestFirstThenShuffle(holders, "10.0.1.20")
	require.NoError(t, err)
	require.Equal(t, "10.0.1.10:5001", got[0])
}

func TestDiffSets(t *testing.T) {
	t.Parallel()

	onlyA, onlyB := diffSets([]string{"a", "b", "b"}, []string{"b", "c"})
	require.ElementsMatch(t, []string{"a"}, onlyA)
	require.ElementsMatch(t, []string{"c"}, onlyB)
}
