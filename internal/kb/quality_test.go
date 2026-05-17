package kb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type recordingQualityRecorder struct {
	events []agentquality.Event
}

func (r *recordingQualityRecorder) RecordKBQualityEvent(sessionID string, event agentquality.Event) {
	r.events = append(r.events, event)
}

func TestSectionTextRecordsQualityEventsForSuccessAndFailure(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	recorder := &recordingQualityRecorder{}
	service := NewService(store, WithQualityRecorder(recorder))

	_, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"0001"},
	})
	require.NoError(t, err)
	require.Len(t, recorder.events, 1)
	assert.Equal(t, agentquality.EventKBRetrieval, recorder.events[0].Name)
	assert.Equal(t, agentquality.StatusPass, recorder.events[0].FinalStatus)
	assert.Equal(t, agentquality.FailureNone, recorder.events[0].FailureType)
	assert.Equal(t, doc.ID, recorder.events[0].Attributes["doc_id"])
	assert.Equal(t, 1, recorder.events[0].Attributes["returned_sections"])

	_, err = service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"missing"},
	})
	require.Error(t, err)
	require.Len(t, recorder.events, 2)
	assert.Equal(t, agentquality.StatusFail, recorder.events[1].FinalStatus)
	assert.Equal(t, agentquality.FailureKBRetrieval, recorder.events[1].FailureType)
	assert.Equal(t, agentquality.KBFailureSectionText, recorder.events[1].Attributes["kb_failure_type"])
	assert.Contains(t, recorder.events[1].Attributes, "error")
}
