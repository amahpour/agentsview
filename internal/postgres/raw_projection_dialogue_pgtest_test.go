//go:build pgtest

package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
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
	// An older saved payload and physical rows both lack the projection.
	parsed.Messages[1].DialogueText = new("answerneedle")
	parsed.Messages[2].DialogueText = new("")
	f.runtime.SetMaxOpenConns(1)
	_, err = f.runtime.ExecContext(t.Context(), `SET lock_timeout='1s'`)
	require.NoError(t, err)
	for _, stage := range []string{"old payload", "no-op with locked message", "current payload with stale physical rows"} {
		t.Run(stage, func(t *testing.T) {
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
			if stage == "no-op with locked message" {
				locked, err := f.admin.BeginTx(t.Context(), nil)
				require.NoError(t, err)
				defer locked.Rollback()
				var ordinal int
				require.NoError(t, locked.QueryRowContext(t.Context(), `SELECT ordinal FROM messages WHERE session_id=$1 AND ordinal=1 FOR UPDATE`, before.SessionID).Scan(&ordinal))
			}
			require.NoError(t, f.sink.Project(t.Context(), leases[0], m, outcome))
			after, err := f.sink.Resolve(t.Context(), "codex:portable")
			require.NoError(t, err)
			assert.Equal(t, before.SessionID, after.SessionID)
			var gotRevision string
			var gotRevisions, gotOutbox int
			require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT raw_content_revision FROM sessions WHERE id=$1`, after.SessionID).Scan(&gotRevision))
			require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_content_revisions`).Scan(&gotRevisions))
			require.NoError(t, f.runtime.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM raw_embedding_outbox`).Scan(&gotOutbox))
			assert.Equal(t, revision, gotRevision)
			assert.Equal(t, revisions, gotRevisions)
			assert.Equal(t, outbox, gotOutbox)
			for _, pattern := range []string{"answerneedle", "reasonneedle", "commandneedle", "toolneedle"} {
				page, err := store.SearchContent(t.Context(), db.ContentSearchFilter{Pattern: pattern, Sources: []string{"messages"}, IncludeOneShot: true})
				require.NoError(t, err)
				if pattern == "answerneedle" {
					require.Len(t, page.Matches, 1)
					assert.Equal(t, "answerneedle", page.Matches[0].Snippet)
				} else {
					assert.Empty(t, page.Matches, pattern)
				}
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
			require.NotNil(t, payload.Messages[1].DialogueText)
			assert.Equal(t, "answerneedle", *payload.Messages[1].DialogueText)
			require.NotNil(t, payload.Messages[2].DialogueText)
			assert.Empty(t, *payload.Messages[2].DialogueText)
			require.NoError(t, tx.Commit())
		})
	}
}
