//go:build pgtest

package postgres

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/dbtest"
)

// Keep PostgreSQL's cross-block and nullable tool-row coverage beside its tests.
func TestPGDialogueSearchAdditionalParity(t *testing.T) {
	syncer, local, _, ctx := newSessionProvenancePushSync(t, "agentsview_dialogue_additional_test")
	const id = "dialogue-search"
	messages := []db.Message{
		{Role: "user", Content: "userneedle", DialogueText: new("userneedle")},
		{Role: "assistant", Content: "[Thinking]\nreasonneedle sharedneedle\n[/Thinking]\nSure.\n[Bash] $ commandneedle\nDone. sharedneedle", ThinkingText: "reasonneedle sharedneedle", HasThinking: true, HasToolUse: true, DialogueText: new("Sure.\nDone. sharedneedle")},
		{Role: "assistant", Content: "[Thinking]\nlegacyneedle\n[/Thinking]\nlegacy answer", ThinkingText: "legacyneedle", HasThinking: true},
		{Role: "tool", Content: "classifiedtoolneedle", DialogueText: new("")},
		{Role: "tool", Content: "legacytoolneedle"},
		{Role: "assistant", Content: "separatereason answer", ThinkingText: "separateneedle", HasThinking: true},
		{Role: "assistant", Content: "systemneedle", ThinkingText: "systemneedle", DialogueText: new("systemneedle"), IsSystem: true},
	}
	for i := range messages {
		messages[i].SessionID = id
		messages[i].Ordinal = i
		messages[i].ContentLength = len(messages[i].Content)
	}
	dbtest.SeedSessionWithMessages(t, local, id, "dialogue", messages, func(s *db.Session) {
		s.MessageCount = len(messages)
		s.UserMessageCount = 2
	})

	_, err := syncer.Push(ctx, true, nil)
	require.NoError(t, err)
	store, err := NewStore(testPGURL(t), syncer.schema, true)
	require.NoError(t, err)
	defer store.Close()
	type hit struct {
		Location, Session string
		Ordinal           int
		Snippet           string
	}
	hits := func(page db.ContentSearchPage) []hit {
		out := make([]hit, 0, len(page.Matches))
		for _, m := range page.Matches {
			out = append(out, hit{m.Location, m.SessionID, m.Ordinal, m.Snippet})
		}
		return out
	}
	for _, mode := range []string{"substring", "regex", "fts", "terms"} {
		sources := [][]string{{"messages"}}
		if mode == "substring" || mode == "regex" {
			sources = append(sources, []string{"thinking"}, []string{"messages", "thinking"})
		}
		for _, source := range sources {
			for _, pattern := range []string{"reasonneedle", "commandneedle", "Sure.", "sharedneedle", "legacyneedle", "classifiedtoolneedle", "legacytoolneedle", "userneedle", "separateneedle", "systemneedle"} {
				t.Run(fmt.Sprintf("%s/%v/%s", mode, source, pattern), func(t *testing.T) {
					f := db.ContentSearchFilter{Pattern: pattern, Mode: mode, Sources: source, Limit: 100, ExcludeSystem: true}
					want, err := local.SearchContent(t.Context(), f)
					require.NoError(t, err)
					got, err := store.SearchContent(t.Context(), f)
					require.NoError(t, err)
					assert.Equal(t, hits(want), hits(got))
				})
			}
		}
	}
	for _, mode := range []string{"substring", "regex"} {
		t.Run(mode+"/cross-block", func(t *testing.T) {
			pattern := "Sure.\nDone."
			if mode == "regex" {
				pattern = `Sure\.\nDone\.`
			}
			f := db.ContentSearchFilter{Pattern: pattern, Mode: mode, Sources: []string{"messages"}}
			want, err := local.SearchContent(t.Context(), f)
			require.NoError(t, err)
			require.Len(t, want.Matches, 1)
			got, err := store.SearchContent(t.Context(), f)
			require.NoError(t, err)
			assert.Equal(t, hits(want), hits(got))
		})
	}
}
