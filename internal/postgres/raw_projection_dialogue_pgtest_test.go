//go:build pgtest

package postgres

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/parser"
)

func TestRawProjectionRefreshesDialogueWithoutNewContent(t *testing.T) {
	f := newProjectionFixture(t)
	_, grantErr := f.admin.Exec(`REVOKE UPDATE ON messages FROM "` + f.role + `"; GRANT UPDATE(dialogue_text) ON messages TO "` + f.role + `"`)
	require.NoError(t, grantErr)
	m, _ := f.accept(t, "device-a", "dialogue-capture", "")
	outcome := projectionOutcome("question")
	const rendered = "[Thinking]\nreasonneedle\n[/Thinking]\nanswerneedle\n[Bash] $ commandneedle"
	parsed := &outcome.Outcome.Results[0].Result
	parsed.Messages[0].DialogueText = new("question")
	parsed.Messages[1].Content = rendered
	parsed.Messages[1].ThinkingText = "reasonneedle"
	parsed.Messages[1].HasThinking = true
	parsed.Messages = append(parsed.Messages, parser.ParsedMessage{Ordinal: 2, Role: parser.RoleTool, Content: "toolneedle"})
	require.NoError(t, f.sink.Project(t.Context(), f.lease(t, m), m, outcome))
	before, err := f.sink.Resolve(t.Context(), "codex:portable")
	require.NoError(t, err)
	require.Equal(t, RawIdentityUnique, before.State)
	require.NoError(t, f.sink.SetPin(t.Context(), "codex:portable", 1, true, "saved note"))
	var revision string
	var revisions, outbox int
	require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT raw_content_revision FROM sessions WHERE id=$1`, before.SessionID).Scan(&revision))
	require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_content_revisions`).Scan(&revisions))
	require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_embedding_outbox`).Scan(&outbox))
	store := &Store{pg: f.runtime}
	// Clear one projection while classifying the assistant and tool rows.
	parsed.Messages[0].DialogueText = nil
	parsed.Messages[1].DialogueText = new("answerneedle")
	parsed.Messages[2].DialogueText = new("")
	f.runtime.SetMaxOpenConns(1)
	_, err = f.runtime.ExecContext(t.Context(), `SET lock_timeout='1s'`)
	require.NoError(t, err)
	for _, stage := range []string{"old payload", "no-op with locked message", "current payload with stale physical rows"} {
		if stage == "current payload with stale physical rows" {
			_, err := f.runtime.ExecContext(t.Context(), `UPDATE messages SET dialogue_text=NULL WHERE session_id=$1`, before.SessionID)
			require.NoError(t, err)
		}
		version := "parser-1+dialogue-v1/" + stage
		_, err := f.sink.SelectSourceGeneration(t.Context(), m, version)
		require.NoError(t, err)
		leases, err := f.jobs.ClaimRawParseJobs(t.Context(), "dialogue-worker", 1, time.Minute)
		require.NoError(t, err)
		require.Len(t, leases, 1)
		var locked *sql.Tx
		if stage == "no-op with locked message" {
			locked, err = f.admin.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer locked.Rollback()
			var ordinal int
			require.NoError(t, locked.QueryRowContext(t.Context(), `SELECT ordinal FROM messages WHERE session_id=$1 AND ordinal=1 FOR UPDATE`, before.SessionID).Scan(&ordinal))
		}
		beforeRefresh, err := f.sink.Resolve(t.Context(), "codex:portable")
		require.NoError(t, err)
		require.NoError(t, f.sink.Project(t.Context(), leases[0], m, outcome))
		if locked != nil {
			require.NoError(t, locked.Rollback())
		}
		after, err := f.sink.Resolve(t.Context(), "codex:portable")
		require.NoError(t, err)
		if stage == "no-op with locked message" {
			assert.Equal(t, beforeRefresh.CorpusRevision, after.CorpusRevision)
		} else {
			assert.Equal(t, beforeRefresh.CorpusRevision+1, after.CorpusRevision)
		}
		assert.Equal(t, before.SessionID, after.SessionID)
		var gotRevision string
		var gotRevisions, gotOutbox int
		require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT raw_content_revision FROM sessions WHERE id=$1`, after.SessionID).Scan(&gotRevision))
		require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_content_revisions`).Scan(&gotRevisions))
		require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_embedding_outbox`).Scan(&gotOutbox))
		assert.Equal(t, revision, gotRevision)
		assert.Equal(t, revisions, gotRevisions)
		assert.Equal(t, outbox, gotOutbox)
		for ordinal, want := range []sql.NullString{{}, {String: "answerneedle", Valid: true}, {Valid: true}} {
			var got sql.NullString
			require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT dialogue_text FROM messages WHERE session_id=$1 AND ordinal=$2`, after.SessionID, ordinal).Scan(&got))
			assert.Equal(t, want, got, "stage %s, ordinal %d", stage, ordinal)
		}
		pins, err := store.ListPinnedMessages(t.Context(), after.SessionID, "")
		require.NoError(t, err)
		require.Len(t, pins, 1)
		require.NotNil(t, pins[0].Note)
		assert.Equal(t, "saved note", *pins[0].Note)
		tx, err := f.runtime.BeginTx(t.Context(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		payload, err := loadRawPayload(t.Context(), tx, after.SessionID)
		require.NoError(t, err)
		assert.Nil(t, payload.Messages[0].DialogueText)
		require.NotNil(t, payload.Messages[1].DialogueText)
		assert.Equal(t, "answerneedle", *payload.Messages[1].DialogueText)
		require.NoError(t, tx.Commit())
	}
}
