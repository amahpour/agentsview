package postgres

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/ingest"
)

func TestRawRecencyDialogueRoundTripAndApply(t *testing.T) {
	state, err := decodeRawRecency([]byte(`{"Dialogue":{"7":"answer","2":"","4":null},"DataVersion":140}`))
	require.NoError(t, err)
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	assert.JSONEq(t, `{"Outcome":"","OutcomeConfidence":"","SignalsPendingSince":null,"HealthScore":null,"HealthGrade":null,"Dialogue":{"7":"answer","2":"","4":null},"DataVersion":140}`, string(encoded))
	roundTrip, err := decodeRawRecency(encoded)
	require.NoError(t, err)
	p := ingest.PreparedSession{Messages: []db.Message{
		{Ordinal: 2, DialogueText: new("stale")},
		{Ordinal: 7},
		{Ordinal: 4, DialogueText: new("stale")},
		{Ordinal: 9, DialogueText: new("payload dialogue")},
	}}
	roundTrip.apply(&p)
	assert.Equal(t, new(""), p.Messages[0].DialogueText)
	assert.Equal(t, new("answer"), p.Messages[1].DialogueText)
	assert.Nil(t, p.Messages[2].DialogueText)
	assert.Nil(t, p.Messages[3].DialogueText, "missing derived entry clears immutable payload dialogue")

	legacy, err := decodeRawRecency([]byte(`{}`))
	require.NoError(t, err)
	legacy.apply(&p)
	assert.Nil(t, p.Messages[0].DialogueText)
	assert.Nil(t, p.Messages[1].DialogueText)
	encoded, err = json.Marshal(legacy)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"Dialogue"`)
	assert.NotContains(t, string(encoded), `"DataVersion"`)
}
