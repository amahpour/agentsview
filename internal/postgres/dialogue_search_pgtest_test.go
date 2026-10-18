//go:build pgtest

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/dbtest"
)

func TestPGDialogueSearchParity(t *testing.T) {
	syncer, local, _, ctx := newSessionProvenancePushSync(t, "agentsview_dialogue_search_test")
	dbtest.SeedDialogueSearch(t, local)
	_, err := syncer.Push(ctx, true, nil)
	require.NoError(t, err)
	store, err := NewStore(testPGURL(t), syncer.schema, true)
	require.NoError(t, err)
	defer store.Close()
	dbtest.AssertDialogueSearchParity(t, local, store)
}
