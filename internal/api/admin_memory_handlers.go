package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/memory"
)

func (s *Server) handleAdminMemoryGovernance(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "memory store 未启用", Code: errs.CodeInternal})
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 1000)
	policy := s.memoryGovernancePolicy(r)
	stats, err := memory.GovernanceStatsForStore(r.Context(), s.memoryStore, memory.GovernanceQueryOptions{
		Search:        memory.SearchOptions{Limit: limit},
		Now:           nowFromQuery(r),
		MinConfidence: parseFloatDefault(r.URL.Query().Get("min_confidence"), policy.MinConfidence),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, withMemoryGovernancePolicy(stats, policy))
}

func (s *Server) handleAdminMemoryPrune(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "memory store 未启用", Code: errs.CodeInternal})
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 1000)
	policy := s.memoryGovernancePolicy(r)
	result, err := s.memoryStore.List(r.Context(), memory.SearchOptions{Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	plan := memory.PlanGovernancePrune(result.Memories, memory.GovernancePruneOptions{
		Now:           nowFromQuery(r),
		MinConfidence: parseFloatDefault(r.URL.Query().Get("min_confidence"), policy.MinConfidence),
		MaxMemories:   parseIntDefault(r.URL.Query().Get("max_memories"), policy.MaxMemories),
	})
	dryRun := r.URL.Query().Get("dry_run") != "false"
	pruned, err := memory.PruneGovernanceForStore(r.Context(), s.memoryStore, plan, dryRun)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, pruned)
}

func (s *Server) handleAdminMemoryExport(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "memory store 未启用", Code: errs.CodeInternal})
		return
	}
	userID := r.URL.Query().Get("user_id")
	limit := parseIntDefault(r.URL.Query().Get("limit"), 1000)
	result, err := s.memoryStore.List(r.Context(), memory.SearchOptions{UserID: userID, Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	data, err := memory.ExportMemoriesJSON(result.Memories, memory.MemoryExportOptions{
		UserID:              userID,
		StrictUserIsolation: userID != "",
		Now:                 time.Now(),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) handleAdminMemoryImport(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "memory store 未启用", Code: errs.CodeInternal})
		return
	}
	var body struct {
		UserID   string          `json:"user_id"`
		ResetIDs bool            `json:"reset_ids"`
		Document json.RawMessage `json:"document"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	// 强制隔离:跨用户写入是注入路径。body.UserID 必填,空一律拒绝。
	// 一次 import 只能写入一个 user_id,且 strict isolation 永远开,
	// 文档里 record.UserID 与 body.UserID 不一致直接拒绝(不静默跳过)。
	if body.UserID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "user_id is required for memory import (cross-user import is not allowed)", Code: errs.CodeBadRequest})
		return
	}
	records, err := memory.ImportMemoriesJSON(body.Document, memory.MemoryImportOptions{
		UserID:              body.UserID,
		StrictUserIsolation: true,
		ResetIDs:            body.ResetIDs,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	var ids []int64
	for i := range records {
		id, err := s.memoryStore.Save(r.Context(), &records[i])
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
			return
		}
		ids = append(ids, id)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": len(ids), "ids": ids})
}

func (s *Server) handleAdminMemoryVectorSpacePlan(w http.ResponseWriter, r *http.Request) {
	if s.memoryStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "memory store 未启用", Code: errs.CodeInternal})
		return
	}
	var body struct {
		TargetSpace string `json:"target_space"`
		BatchSize   int    `json:"batch_size"`
		ResumeToken string `json:"resume_token"`
		Offset      int    `json:"offset"`
		DryRun      *bool  `json:"dry_run"`
		Apply       bool   `json:"apply"`
		Limit       int    `json:"limit"`
		UserID      string `json:"user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Limit <= 0 {
		body.Limit = 1000
	}
	result, err := s.memoryStore.List(r.Context(), memory.SearchOptions{UserID: body.UserID, Limit: body.Limit})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	apply := body.Apply || (body.DryRun != nil && !*body.DryRun)
	plan := memory.PlanVectorSpaceMigration(result.Memories, memory.VectorSpaceMigrationOptions{
		TargetSpace: body.TargetSpace,
		BatchSize:   body.BatchSize,
		ResumeToken: body.ResumeToken,
		Offset:      body.Offset,
		DryRun:      !apply,
		Now:         time.Now(),
	})
	if apply {
		if s.memoryEmbeddingBacklog == nil {
			s.memoryEmbeddingBacklog = memory.NewInMemoryEmbeddingBacklog()
		}
		for _, update := range plan.Updates {
			if err := s.memoryStore.Update(r.Context(), &update.Record); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
				return
			}
			_, _ = s.memoryEmbeddingBacklog.Enqueue(r.Context(), memory.EmbeddingBacklogJob{
				MemoryID:    update.Record.ID,
				UserID:      update.Record.UserID,
				Content:     update.Record.Content,
				VectorSpace: memory.DecodeVectorSpace(update.Record.Metadata).Name,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":    plan,
		"applied": apply,
		"updated": len(plan.Updates),
	})
}

func (s *Server) handleAdminMemoryBacklogStats(w http.ResponseWriter, r *http.Request) {
	if s.memoryEmbeddingBacklog == nil {
		s.memoryEmbeddingBacklog = memory.NewInMemoryEmbeddingBacklog()
	}
	stats, err := s.memoryEmbeddingBacklog.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

type memoryGovernancePolicyDTO struct {
	MinConfidence float64 `json:"min_confidence"`
	MaxMemories   int     `json:"max_memories"`
}

type memoryGovernanceResponse struct {
	memory.GovernanceStats
	Policy memoryGovernancePolicyDTO `json:"policy"`
}

func withMemoryGovernancePolicy(stats memory.GovernanceStats, policy memoryGovernancePolicyDTO) memoryGovernanceResponse {
	return memoryGovernanceResponse{GovernanceStats: stats, Policy: policy}
}

func (s *Server) memoryGovernancePolicy(r *http.Request) memoryGovernancePolicyDTO {
	policy := memoryGovernancePolicyDTO{MinConfidence: 0.5}
	if s.memoryGovernancePolicyStore == nil {
		return policy
	}
	raw, ok, err := s.memoryGovernancePolicyStore.GetMemoryGovernancePolicy(r.Context(), "default")
	if err != nil || !ok {
		return policy
	}
	_ = json.Unmarshal([]byte(raw), &policy)
	if policy.MinConfidence == 0 {
		policy.MinConfidence = 0.5
	}
	return policy
}

func parseIntDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}

func parseFloatDefault(raw string, def float64) float64 {
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return v
}

func nowFromQuery(_ *http.Request) time.Time {
	return time.Now()
}
