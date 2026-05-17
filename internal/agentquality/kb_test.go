package agentquality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewKBQualityEventUsesAttributesForHighCardinalityFields(t *testing.T) {
	ev := NewKBQualityEvent(KBEventInput{
		Name:       EventKBRetrieval,
		SessionID:  "session-1",
		TurnID:     "turn-1",
		TraceID:    "trace-1",
		ToolCallID: "call-1",
		DomainID:   "support",
		OwnerScope: OwnerScopeUser,
		OwnerID:    "user-1",
		Route:      "web",
		ToolName:   "kb.section.text",
		Status:     StatusFail,
		Failure:    FailureKBRetrieval,
		Reason:     KBFailureSectionText,
		Error:      "not found",
		Attributes: map[string]any{"doc_id": "doc-1", "node_ids": []string{"0001"}},
	})

	assert.Equal(t, EventKBRetrieval, ev.Name)
	assert.Equal(t, FailureKBRetrieval, ev.FailureType)
	assert.Equal(t, "kb.section.text", ev.ToolDecision.Actual)
	assert.Equal(t, "doc-1", ev.Attributes["doc_id"])
	assert.Equal(t, KBFailureSectionText, ev.Attributes["kb_failure_type"])

	labels := MetricLabels(ev)
	assert.Equal(t, "web", labels["route"])
	assert.NotContains(t, labels, "doc_id")
	assert.NotContains(t, labels, "node_ids")
}

func TestScoreKBPageIndexRetrievalRequiresExpectedNodesAndPages(t *testing.T) {
	score := ScoreKBPageIndexRetrieval(KBPageIndexRetrievalCase{
		CaseID:             "kb-pageindex-1",
		ExpectedNodeIDs:    []string{"0001", "0002"},
		ExpectedPageRanges: []string{"5 - 7"},
	}, KBPageIndexRetrievalActual{
		RetrievedNodeIDs:    []string{"0002", "0003"},
		RetrievedPageRanges: []string{"5-7"},
		CitationNodeIDs:     []string{"0002"},
		CitationPageRanges:  []string{"5-7"},
	})

	assert.False(t, score.RetrievalHit)
	assert.False(t, score.CitationHit)
	assert.Equal(t, []string{"0001"}, score.MissingNodeIDs)
	assert.Empty(t, score.MissingPageRanges)
	assert.Equal(t, []string{"0001"}, score.MissingCitationNodes)
	assert.Empty(t, score.MissingCitationPages)
	assert.Equal(t, []string{"0003"}, score.UnexpectedNodeIDs)
	assert.Equal(t, 3, score.ExpectedCount)
	assert.Equal(t, 3, score.RetrievedCount)
}

func TestScoreKBPageIndexRetrievalRequiresCitationPages(t *testing.T) {
	score := ScoreKBPageIndexRetrieval(KBPageIndexRetrievalCase{
		CaseID:             "kb-pageindex-page-citation",
		ExpectedPageRanges: []string{"9-10"},
	}, KBPageIndexRetrievalActual{
		RetrievedPageRanges: []string{"9-10"},
		CitationPageRanges:  []string{"9"},
	})

	assert.True(t, score.RetrievalHit)
	assert.False(t, score.CitationHit)
	assert.Equal(t, []string{"9-10"}, score.MissingCitationPages)
}

func TestScoreKBPageIndexRetrievalSummarizesHitRates(t *testing.T) {
	scores := []KBPageIndexRetrievalScore{
		ScoreKBPageIndexRetrieval(KBPageIndexRetrievalCase{CaseID: "hit", ExpectedNodeIDs: []string{"0001"}}, KBPageIndexRetrievalActual{RetrievedNodeIDs: []string{"0001"}, CitationNodeIDs: []string{"0001"}}),
		ScoreKBPageIndexRetrieval(KBPageIndexRetrievalCase{CaseID: "miss", ExpectedPageRanges: []string{"8"}}, KBPageIndexRetrievalActual{RetrievedPageRanges: []string{"9"}}),
	}

	summary := SummarizeKBPageIndexRetrieval(scores)
	assert.Equal(t, 2, summary["total"])
	assert.Equal(t, 1, summary["retrieval_hits"])
	assert.Equal(t, 0.5, summary["retrieval_hit_rate"])
	assert.Equal(t, "kb.doc.meta -> kb.doc.structure -> kb.section.text(node_ids|page_ranges)", summary["retrieval_contract"])
}
