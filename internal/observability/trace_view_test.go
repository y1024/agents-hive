package observability

import (
	"testing"
	"time"
)

func TestSortTraceTimelineItems(t *testing.T) {
	t2 := time.Date(2026, 5, 6, 10, 0, 2, 0, time.UTC)
	t1 := time.Date(2026, 5, 6, 10, 0, 1, 0, time.UTC)
	items := []TraceTimelineItem{
		{Operation: "b", Timestamp: t2},
		{Operation: "a", Timestamp: t1},
	}

	SortTraceTimelineItems(items)

	if items[0].Operation != "a" {
		t.Fatalf("first operation = %q, want a", items[0].Operation)
	}
}

func TestSortTraceTimelineItemsUsesStableTieBreakers(t *testing.T) {
	ts := time.Date(2026, 5, 6, 10, 0, 1, 0, time.UTC)
	items := []TraceTimelineItem{
		{TraceID: "trace-b", SpanID: "span-b", Operation: "b", Timestamp: ts},
		{TraceID: "trace-a", SpanID: "span-c", Operation: "c", Timestamp: ts},
		{TraceID: "trace-a", SpanID: "span-a", Operation: "a", Timestamp: ts},
	}

	SortTraceTimelineItems(items)

	if items[0].SpanID != "span-a" || items[1].SpanID != "span-c" || items[2].TraceID != "trace-b" {
		t.Fatalf("stable order mismatch: %+v", items)
	}
}
