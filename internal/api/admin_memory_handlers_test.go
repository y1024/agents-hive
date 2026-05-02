package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/memory"
)

func TestAdminMemoryGovernance_ReturnsStats(t *testing.T) {
	now := time.Now()
	store := &fakeAdminMemoryStore{records: []memory.MemoryRecord{
		{ID: 1, Metadata: memory.EncodeGovernance(nil, memory.Governance{Confidence: 0.9, ExpiresAt: now.Add(time.Hour)})},
		{ID: 2},
	}}
	srv := newAdminMemoryTestServer(store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/memory/governance", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminMemoryGovernance(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got memory.GovernanceStats
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Equal(t, 2, got.Total)
	require.Equal(t, 1, got.MissingGovernance)
}

func TestAdminMemoryPruneDryRunDoesNotDelete(t *testing.T) {
	now := time.Now()
	store := &fakeAdminMemoryStore{records: []memory.MemoryRecord{
		{ID: 1, Metadata: memory.EncodeGovernance(nil, memory.Governance{Confidence: 0.9, ExpiresAt: now.Add(-time.Hour)})},
	}}
	srv := newAdminMemoryTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/prune?dry_run=true", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminMemoryPrune(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, store.deleted)
	var got struct {
		DryRun    bool    `json:"dry_run"`
		DeleteIDs []int64 `json:"delete_ids"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.True(t, got.DryRun)
	require.Equal(t, []int64{1}, got.DeleteIDs)
}

func TestAdminMemoryPruneDeletesWhenExplicit(t *testing.T) {
	now := time.Now()
	store := &fakeAdminMemoryStore{records: []memory.MemoryRecord{
		{ID: 1, Metadata: memory.EncodeGovernance(nil, memory.Governance{Confidence: 0.9, ExpiresAt: now.Add(-time.Hour)})},
	}}
	srv := newAdminMemoryTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/prune?dry_run=false", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminMemoryPrune(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []int64{1}, store.deleted)
}

func TestAdminMemoryGovernanceUsesPersistedPolicyDefaults(t *testing.T) {
	store := &fakeAdminMemoryStore{records: []memory.MemoryRecord{
		{ID: 1, Metadata: memory.EncodeGovernance(nil, memory.Governance{Confidence: 0.4, ExpiresAt: time.Now().Add(time.Hour)})},
	}}
	policies := newFakeOptimizationWritableStore()
	require.NoError(t, policies.UpsertMemoryGovernancePolicy(context.Background(), "default", `{"min_confidence":0.5,"max_memories":10}`, "test"))
	srv := newAdminMemoryTestServer(store)
	srv.memoryGovernancePolicyStore = policies

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/memory/governance", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminMemoryGovernance(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got struct {
		memory.GovernanceStats
		Policy struct {
			MinConfidence float64 `json:"min_confidence"`
			MaxMemories   int     `json:"max_memories"`
		} `json:"policy"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Equal(t, 1, got.LowConfidence)
	require.Equal(t, 0.5, got.Policy.MinConfidence)
	require.Equal(t, 10, got.Policy.MaxMemories)
}

func TestAdminMemoryExportImportAndVectorSpaceApply(t *testing.T) {
	store := &fakeAdminMemoryStore{records: []memory.MemoryRecord{
		{ID: 1, UserID: "u1", Type: memory.MemoryTypeUser, Content: "alpha", Tags: []string{"a"}, Metadata: json.RawMessage(`{}`)},
	}}
	srv := newAdminMemoryTestServer(store)

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/memory/export?user_id=u1", nil)
	exportOut := httptest.NewRecorder()
	srv.handleAdminMemoryExport(exportOut, exportReq)
	require.Equal(t, http.StatusOK, exportOut.Code, exportOut.Body.String())
	require.Contains(t, exportOut.Body.String(), `"memories"`)

	importBody := `{"user_id":"u1","reset_ids":true,"document":` + exportOut.Body.String() + `}`
	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/import", strings.NewReader(importBody))
	importOut := httptest.NewRecorder()
	srv.handleAdminMemoryImport(importOut, importReq)
	require.Equal(t, http.StatusCreated, importOut.Code, importOut.Body.String())
	var imported struct {
		Imported int     `json:"imported"`
		IDs      []int64 `json:"ids"`
	}
	require.NoError(t, json.NewDecoder(importOut.Body).Decode(&imported))
	require.Equal(t, 1, imported.Imported)
	require.NotEmpty(t, imported.IDs)

	dryRunReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/vector-space/plan", strings.NewReader(`{"target_space":"memory:v2","batch_size":10}`))
	dryRunOut := httptest.NewRecorder()
	srv.handleAdminMemoryVectorSpacePlan(dryRunOut, dryRunReq)
	require.Equal(t, http.StatusOK, dryRunOut.Code, dryRunOut.Body.String())
	require.Empty(t, store.updated)

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/vector-space/plan", strings.NewReader(`{"target_space":"memory:v2","apply":true,"batch_size":10}`))
	applyOut := httptest.NewRecorder()
	srv.handleAdminMemoryVectorSpacePlan(applyOut, applyReq)
	require.Equal(t, http.StatusOK, applyOut.Code, applyOut.Body.String())
	require.NotEmpty(t, store.updated)
	require.Equal(t, "memory:v2", memory.DecodeVectorSpace(store.updated[0].Metadata).Name)

	statsReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/memory/backlog/stats", nil)
	statsOut := httptest.NewRecorder()
	srv.handleAdminMemoryBacklogStats(statsOut, statsReq)
	require.Equal(t, http.StatusOK, statsOut.Code, statsOut.Body.String())
	var stats memory.EmbeddingBacklogStats
	require.NoError(t, json.NewDecoder(statsOut.Body).Decode(&stats))
	require.GreaterOrEqual(t, stats.Total, 1)
	job, ok := srv.memoryEmbeddingBacklog.(*memory.InMemoryEmbeddingBacklog).Get(1)
	require.True(t, ok)
	require.Equal(t, "memory:v2", job.VectorSpace)
}

func TestAdminMemoryImportRejectsCrossUserInjection(t *testing.T) {
	store := &fakeAdminMemoryStore{}
	srv := newAdminMemoryTestServer(store)

	// 攻击向量:admin 不传 body.user_id,JSONL 文档里手工写多个 user_id。
	// 期望:handler 必须 400 拒绝,不写任何 record。
	docWithCrossUser := `{
        "version": 1,
        "memories": [
            {"id":1,"user_id":"victim-a","type":"user","content":"injected"},
            {"id":2,"user_id":"victim-b","type":"user","content":"injected2"}
        ]
    }`
	body := `{"reset_ids":true,"document":` + docWithCrossUser + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/import", strings.NewReader(body))
	out := httptest.NewRecorder()
	srv.handleAdminMemoryImport(out, req)

	require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "user_id is required")
	require.Empty(t, store.records, "no record may be saved when user_id missing")

	// admin 显式传 user_id,但文档里某条 record.user_id 不一致 → strict isolation 拒绝整批,不静默跳过。
	bodyMismatch := `{"user_id":"target","reset_ids":true,"document":` + docWithCrossUser + `}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/memory/import", strings.NewReader(bodyMismatch))
	out2 := httptest.NewRecorder()
	srv.handleAdminMemoryImport(out2, req2)
	require.Equal(t, http.StatusBadRequest, out2.Code, out2.Body.String())
	require.Empty(t, store.records, "no record may be saved when document contains mismatched user_id")
}

type fakeAdminMemoryStore struct {
	records []memory.MemoryRecord
	deleted []int64
	updated []memory.MemoryRecord
	nextID  int64
}

func (s *fakeAdminMemoryStore) Save(context.Context, *memory.MemoryRecord) (int64, error) {
	s.nextID++
	return s.nextID, nil
}
func (s *fakeAdminMemoryStore) Get(context.Context, int64) (*memory.MemoryRecord, error) {
	return nil, nil
}
func (s *fakeAdminMemoryStore) Update(_ context.Context, rec *memory.MemoryRecord) error {
	s.updated = append(s.updated, *rec)
	for i := range s.records {
		if s.records[i].ID == rec.ID {
			s.records[i] = *rec
			return nil
		}
	}
	s.records = append(s.records, *rec)
	return nil
}
func (s *fakeAdminMemoryStore) Delete(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, id)
	return nil
}
func (s *fakeAdminMemoryStore) Search(context.Context, memory.SearchOptions) (*memory.SearchResult, error) {
	return &memory.SearchResult{}, nil
}
func (s *fakeAdminMemoryStore) List(context.Context, memory.SearchOptions) (*memory.SearchResult, error) {
	return &memory.SearchResult{Memories: s.records, Total: len(s.records)}, nil
}
func (s *fakeAdminMemoryStore) Stats(context.Context) (*memory.MemoryStats, error) {
	return &memory.MemoryStats{}, nil
}
func (s *fakeAdminMemoryStore) SetEmbedding(memory.EmbeddingProvider, memory.VectorStore) {}
func (s *fakeAdminMemoryStore) Close() error                                              { return nil }

func newAdminMemoryTestServer(store memory.MemoryStore) *Server {
	return &Server{logger: zap.NewNop(), config: config.Default(), memoryStore: store}
}
