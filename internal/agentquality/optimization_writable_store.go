package agentquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InMemoryOptimizationWritableStore struct {
	mu               sync.RWMutex
	ToolDescriptions map[string]string
	MemoryPolicies   map[string]string
}

func NewInMemoryOptimizationWritableStore() *InMemoryOptimizationWritableStore {
	return &InMemoryOptimizationWritableStore{
		ToolDescriptions: map[string]string{},
		MemoryPolicies:   map[string]string{},
	}
}

func (s *InMemoryOptimizationWritableStore) UpsertToolDescription(ctx context.Context, toolName, description, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(toolName) == "" {
		return fmt.Errorf("tool name is required")
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("tool description is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ToolDescriptions == nil {
		s.ToolDescriptions = map[string]string{}
	}
	s.ToolDescriptions[strings.TrimSpace(toolName)] = strings.TrimSpace(description)
	return nil
}

func (s *InMemoryOptimizationWritableStore) GetToolDescription(ctx context.Context, toolName string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return "", false, fmt.Errorf("tool name is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.ToolDescriptions[name]
	return value, ok, nil
}

func (s *InMemoryOptimizationWritableStore) DeleteToolDescription(ctx context.Context, toolName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ToolDescriptions, name)
	return nil
}

func (s *InMemoryOptimizationWritableStore) UpsertMemoryGovernancePolicy(ctx context.Context, policyName, policyJSON, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	if err := validateJSON(policyJSON); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MemoryPolicies == nil {
		s.MemoryPolicies = map[string]string{}
	}
	s.MemoryPolicies[name] = strings.TrimSpace(policyJSON)
	return nil
}

func (s *InMemoryOptimizationWritableStore) GetMemoryGovernancePolicy(ctx context.Context, policyName string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.MemoryPolicies[name]
	return value, ok, nil
}

func (s *InMemoryOptimizationWritableStore) DeleteMemoryGovernancePolicy(ctx context.Context, policyName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.MemoryPolicies, name)
	return nil
}

type PGOptimizationWritableStore struct {
	pool *pgxpool.Pool
}

func NewPGOptimizationWritableStore(pool *pgxpool.Pool) *PGOptimizationWritableStore {
	return &PGOptimizationWritableStore{pool: pool}
}

func (s *PGOptimizationWritableStore) UpsertToolDescription(ctx context.Context, toolName, description, updatedBy string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(toolName)
	desc := strings.TrimSpace(description)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if desc == "" {
		return fmt.Errorf("tool description is required")
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO optimization_tool_descriptions (tool_name, description, updated_by, updated_at)
VALUES ($1,$2,$3,$4)
ON CONFLICT (tool_name)
DO UPDATE SET description=$2, updated_by=$3, updated_at=$4`,
		name, desc, strings.TrimSpace(updatedBy), time.Now(),
	)
	return err
}

func (s *PGOptimizationWritableStore) GetToolDescription(ctx context.Context, toolName string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return "", false, fmt.Errorf("tool name is required")
	}
	var value string
	if err := s.pool.QueryRow(ctx, `SELECT description FROM optimization_tool_descriptions WHERE tool_name=$1`, name).Scan(&value); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (s *PGOptimizationWritableStore) DeleteToolDescription(ctx context.Context, toolName string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM optimization_tool_descriptions WHERE tool_name=$1`, name)
	return err
}

func (s *PGOptimizationWritableStore) UpsertMemoryGovernancePolicy(ctx context.Context, policyName, policyJSON, updatedBy string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	body := strings.TrimSpace(policyJSON)
	if err := validateJSON(body); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO memory_governance_policies (name, policy_json, updated_by, updated_at)
VALUES ($1,$2,$3,$4)
ON CONFLICT (name)
DO UPDATE SET policy_json=$2, updated_by=$3, updated_at=$4`,
		name, body, strings.TrimSpace(updatedBy), time.Now(),
	)
	return err
}

func (s *PGOptimizationWritableStore) GetMemoryGovernancePolicy(ctx context.Context, policyName string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	var value string
	if err := s.pool.QueryRow(ctx, `SELECT policy_json::text FROM memory_governance_policies WHERE name=$1`, name).Scan(&value); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (s *PGOptimizationWritableStore) DeleteMemoryGovernancePolicy(ctx context.Context, policyName string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("optimization writable store not configured")
	}
	name := strings.TrimSpace(policyName)
	if name == "" {
		name = "default"
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM memory_governance_policies WHERE name=$1`, name)
	return err
}

func validateJSON(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("policy json is required")
	}
	var v any
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		return fmt.Errorf("invalid policy json: %w", err)
	}
	return nil
}
