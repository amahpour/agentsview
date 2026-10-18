//go:build pgtest

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/dbtest"
)

func TestPGPushRepushesDialogueOnlyChanges(t *testing.T) {
	syncer, local, pg, ctx := newSessionProvenancePushSync(t, "agentsview_dialogue_repush_test")
	dbtest.SeedDialogueSearch(t, local)
	_, err := syncer.Push(ctx, true, nil)
	require.NoError(t, err)
	unchanged, err := syncer.Push(ctx, false, nil)
	require.NoError(t, err)
	assert.Zero(t, unchanged.SessionsPushed)
	session, err := local.GetSession(ctx, "dialogue-search")
	require.NoError(t, err)
	require.NotNil(t, session)
	messages, err := local.GetMessages(ctx, session.ID, 0, 100, true)
	require.NoError(t, err)
	messages[1].DialogueText = new("updated\x00dialogue\xe2")
	session.DataVersion = db.CurrentDataVersion()
	_, err = local.WriteSessionBatchContext(ctx, []db.SessionBatchWrite{{Session: *session, Messages: messages, DataVersion: db.CurrentDataVersion(), ReplaceMessages: true}})
	require.NoError(t, err)
	result, err := syncer.Push(ctx, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.SessionsPushed)
	var dialogue string
	require.NoError(t, pg.QueryRowContext(ctx, `SELECT dialogue_text FROM messages WHERE session_id=$1 AND ordinal=1`, session.ID).Scan(&dialogue))
	assert.Equal(t, "updateddialogue", dialogue)
	unchanged, err = syncer.Push(ctx, false, nil)
	require.NoError(t, err)
	assert.Zero(t, unchanged.SessionsPushed)

	// The single-session path is also used outside the batched push loop.
	_, err = pg.ExecContext(ctx, `UPDATE messages SET dialogue_text='stale' WHERE session_id=$1 AND ordinal=1`, session.ID)
	require.NoError(t, err)
	tx, err := pg.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	count, err := syncer.pushMessages(ctx, tx, session.ID, false, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, len(messages), count)
	require.NoError(t, tx.Commit())
	require.NoError(t, pg.QueryRowContext(ctx, `SELECT dialogue_text FROM messages WHERE session_id=$1 AND ordinal=1`, session.ID).Scan(&dialogue))
	assert.Equal(t, "updateddialogue", dialogue)
}

func TestPGDialogueBackfillPerScope(t *testing.T) {
	unfiltered, local, pg, ctx := newSessionProvenancePushSync(t, "agentsview_dialogue_backfill_test")
	dbtest.SeedDialogueSearch(t, local)
	projects := []string{"dialogue"}
	scope := pushSyncStateScope("", projects, nil)
	filtered := &Sync{pg: pg, local: local, machine: unfiltered.machine, schema: unfiltered.schema, schemaDone: true,
		syncState: newScopedSyncStateStore(local, scope, false), syncStateTarget: scope, projects: projects,
		aliasBackfillState: newScopedSyncStateStore(local, "", false)}
	_, err := filtered.Push(ctx, false, nil)
	require.NoError(t, err)
	_, err = pg.ExecContext(ctx, `UPDATE messages SET dialogue_text=NULL WHERE session_id='dialogue-search'`)
	require.NoError(t, err)
	require.NoError(t, filtered.syncState.SetSyncState(ctx, messageDialogueBackfillStateKey, ""))
	_, err = filtered.Push(ctx, false, nil)
	require.NoError(t, err)
	marker, err := filtered.syncState.GetSyncState(ctx, messageDialogueBackfillStateKey)
	require.NoError(t, err)
	assert.Equal(t, "1", marker)
	marker, err = local.GetSyncState(ctx, messageDialogueBackfillStateKey)
	require.NoError(t, err)
	assert.Empty(t, marker)
	_, err = unfiltered.Push(ctx, false, nil)
	require.NoError(t, err)
	marker, err = local.GetSyncState(ctx, messageDialogueBackfillStateKey)
	require.NoError(t, err)
	assert.Equal(t, "1", marker)
}
