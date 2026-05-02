package bootstrap

import (
	"context"
	"errors"
	"testing"
)

func TestDrainEmbeddingBacklogWorkerKeepsProcessingUntilIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &fakeEmbeddingBacklogProcessor{results: []embeddingBacklogProcessResult{
		{processed: true},
		{processed: true},
		{processed: false},
	}}

	drainEmbeddingBacklogWorker(ctx, worker, nil)

	if worker.calls != 3 {
		t.Fatalf("ProcessOne calls = %d, want 3", worker.calls)
	}
}

func TestDrainEmbeddingBacklogWorkerStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &fakeEmbeddingBacklogProcessor{afterCall: cancel}

	drainEmbeddingBacklogWorker(ctx, worker, nil)

	if worker.calls != 1 {
		t.Fatalf("ProcessOne calls = %d, want 1", worker.calls)
	}
}

func TestDrainEmbeddingBacklogWorkerTreatsErroredJobAsProcessed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &fakeEmbeddingBacklogProcessor{results: []embeddingBacklogProcessResult{
		{processed: true, err: errors.New("embed failed")},
		{processed: false},
	}}

	drainEmbeddingBacklogWorker(ctx, worker, nil)

	if worker.calls != 2 {
		t.Fatalf("ProcessOne calls = %d, want 2", worker.calls)
	}
}

type embeddingBacklogProcessResult struct {
	processed bool
	err       error
}

type fakeEmbeddingBacklogProcessor struct {
	results   []embeddingBacklogProcessResult
	afterCall func()
	calls     int
}

func (w *fakeEmbeddingBacklogProcessor) ProcessOne(context.Context) (bool, error) {
	w.calls++
	if w.afterCall != nil {
		w.afterCall()
	}
	if len(w.results) == 0 {
		return false, nil
	}
	next := w.results[0]
	w.results = w.results[1:]
	return next.processed, next.err
}
