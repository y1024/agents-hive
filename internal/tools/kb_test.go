package tools

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/kb"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/toolctx"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
)

var llmProviderToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type fakeKBService struct {
	resolveInput kb.BindingResolveInput
	scope        kb.Scope
	evidence     kb.EvidenceScope
	metaInput    kb.DocMetaInput
	sectionInput kb.SectionTextInput
	namespaces   []string
	resolveErr   error
}

func (s *fakeKBService) ResolveBinding(ctx context.Context, input kb.BindingResolveInput) ([]string, error) {
	s.resolveInput = input
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}
	return append([]string(nil), s.namespaces...), nil
}

func (s *fakeKBService) DocMeta(ctx context.Context, scope kb.Scope, input kb.DocMetaInput) (*kb.DocMetaResult, error) {
	s.scope = scope
	s.metaInput = input
	return &kb.DocMetaResult{Documents: []kb.DocMetaDocument{{ID: "doc-1", NamespaceID: "ns-1", Title: "Doc", Status: kb.DocumentActive}}}, nil
}

func (s *fakeKBService) DocStructure(ctx context.Context, scope kb.Scope, input kb.DocStructureInput) (*kb.DocStructureResult, error) {
	s.scope = scope
	return &kb.DocStructureResult{DocumentID: input.DocumentID, NamespaceID: scope.NamespaceNarrowing}, nil
}

func (s *fakeKBService) SectionText(ctx context.Context, scope kb.Scope, evidence kb.EvidenceScope, input kb.SectionTextInput) (*kb.SectionTextResult, error) {
	s.scope = scope
	s.evidence = evidence
	s.sectionInput = input
	nodeID := ""
	if len(input.NodeIDs) > 0 {
		nodeID = input.NodeIDs[0]
	}
	return &kb.SectionTextResult{
		DocumentID:  input.DocumentID,
		NamespaceID: scope.NamespaceNarrowing,
		Sections: []kb.SectionText{{
			NodeID:        nodeID,
			NodePath:      "0000",
			Title:         "Intro",
			Text:          "Evidence text",
			EvidenceToken: "kbref-token",
		}},
	}, nil
}

func TestKBToolsRegisteredWithRecoverableNilService(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	RegisterBuiltinTools(host, logger, nil, nil, nil, "", nil, nil, nil, nil, nil)

	def, err := host.GetTool("kb.doc.meta")
	if err != nil {
		t.Fatalf("kb.doc.meta should be registered without KB service: %v", err)
	}
	if !def.Core || !def.IsConcurrencySafe {
		t.Fatalf("kb.doc.meta definition = %+v, want core concurrency-safe", def)
	}

	result, err := host.ExecuteTool(context.Background(), "kb.doc.meta", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool(kb.doc.meta): %v", err)
	}
	if !result.IsError {
		t.Fatal("nil KB service should return tool error")
	}
	if !strings.Contains(result.DecodeContent(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("nil KB service error = %q, want recoverable marker", result.DecodeContent())
	}
}

func TestKBToolNamesAreProviderCompatible(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	registerKBTools(host, logger, nil)

	for _, name := range []string{"kb.doc.meta", "kb.doc.structure", "kb.section.text"} {
		alias := strings.NewReplacer(".", "_", "/", "_", " ", "_").Replace(name)
		if !llmProviderToolNamePattern.MatchString(alias) {
			t.Fatalf("KB tool %q provider alias %q must match OpenAI-compatible function name pattern", name, alias)
		}
	}
}

func TestKBToolSchemasDoNotExposeAuthorizationFields(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	registerKBTools(host, logger, nil)

	for _, name := range []string{"kb.doc.meta", "kb.doc.structure", "kb.section.text"} {
		def, err := host.GetTool(name)
		if err != nil {
			t.Fatalf("GetTool(%s): %v", name, err)
		}
		schema := strings.ToLower(string(def.InputSchema))
		for _, forbidden := range []string{"owner", "domain", "session", "agent", "template", "user_id", "tenant"} {
			if strings.Contains(schema, forbidden) {
				t.Fatalf("%s schema exposes authorization field %q: %s", name, forbidden, schema)
			}
		}
	}
}

func TestKBSectionTextSchemaSupportsPageRanges(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	registerKBTools(host, logger, nil)
	def, err := host.GetTool("kb.section.text")
	if err != nil {
		t.Fatalf("GetTool(kb.section.text): %v", err)
	}
	schema := string(def.InputSchema)
	if !strings.Contains(schema, "page_ranges") {
		t.Fatalf("kb.section.text schema should expose page_ranges: %s", schema)
	}
	if strings.Contains(schema, `"required":["doc_id","node_ids"]`) {
		t.Fatalf("kb.section.text should not require node_ids when page_ranges is available: %s", schema)
	}
}

func TestKBWrapperDerivesScopeAndUsesNamespaceOnlyForNarrowing(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	service := &fakeKBService{namespaces: []string{"ns-1", "ns-2"}}
	registerKBTools(host, logger, service)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Status: "active"})
	ctx = toolctx.WithSessionID(ctx, "session-1")
	ctx = toolctx.WithToolContext(ctx, &toolctx.ToolContext{
		CallerType: toolctx.CallerMaster,
		CallerName: "master",
		TraceID:    "trace-1",
		TurnID:     "turn-1",
		ToolCallID: "call-1",
	})
	ctx = WithKBRuntimeContext(ctx, KBRuntimeContext{
		DomainID:          "support",
		OwnerScope:        kb.OwnerScopeTenant,
		OwnerID:           "tenant-1",
		TenantID:          "tenant-1",
		AgentID:           "agent-1",
		SessionTemplateID: "template-1",
	})

	input := json.RawMessage(`{"namespace_id":"ns-2","doc_id":"doc-1","node_ids":["0000"]}`)
	result, err := host.ExecuteTool(ctx, "kb.section.text", input)
	if err != nil {
		t.Fatalf("ExecuteTool(kb.section.text): %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected KB tool error: %s", result.DecodeContent())
	}

	if service.resolveInput.DomainID != "support" ||
		service.resolveInput.OwnerScope != kb.OwnerScopeTenant ||
		service.resolveInput.OwnerID != "tenant-1" ||
		service.resolveInput.UserID != "user-1" ||
		service.resolveInput.SessionID != "session-1" ||
		service.resolveInput.AgentID != "agent-1" ||
		service.resolveInput.SessionTemplateID != "template-1" {
		t.Fatalf("ResolveBinding input = %+v", service.resolveInput)
	}
	if service.scope.NamespaceNarrowing != "ns-2" || len(service.scope.NamespaceIDs) != 2 {
		t.Fatalf("scope = %+v, want namespace_ids from resolver and narrowing ns-2", service.scope)
	}
	if service.sectionInput.NamespaceID != "ns-2" || service.sectionInput.DocumentID != "doc-1" || len(service.sectionInput.NodeIDs) != 1 {
		t.Fatalf("section input = %+v", service.sectionInput)
	}
	if service.evidence.SessionID != "session-1" || service.evidence.TurnID != "turn-1" || service.evidence.TraceID != "trace-1" || service.evidence.ToolCallID != "call-1" {
		t.Fatalf("evidence scope = %+v", service.evidence)
	}
}

func TestKBWrapperPassesPageRanges(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	service := &fakeKBService{namespaces: []string{"ns-1"}}
	registerKBTools(host, logger, service)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Status: "active"})
	ctx = toolctx.WithSessionID(ctx, "session-1")
	ctx = toolctx.WithToolContext(ctx, &toolctx.ToolContext{TraceID: "trace-1", TurnID: "turn-1", ToolCallID: "call-1"})
	ctx = WithKBRuntimeContext(ctx, KBRuntimeContext{DomainID: "support", OwnerScope: kb.OwnerScopeUser, OwnerID: "user-1"})

	result, err := host.ExecuteTool(ctx, "kb.section.text", json.RawMessage(`{"doc_id":"doc-1","page_ranges":["5-7"]}`))
	if err != nil {
		t.Fatalf("ExecuteTool(kb.section.text): %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected KB tool error: %s", result.DecodeContent())
	}
	if len(service.sectionInput.PageRanges) != 1 || service.sectionInput.PageRanges[0] != "5-7" {
		t.Fatalf("section input page ranges = %+v", service.sectionInput)
	}
}

func TestKBWrapperDoesNotFallbackToGenericDomain(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	service := &fakeKBService{resolveErr: kb.ErrNoKBBinding}
	registerKBTools(host, logger, service)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Status: "active"})
	ctx = toolctx.WithSessionID(ctx, "session-1")
	ctx = toolctx.WithToolContext(ctx, &toolctx.ToolContext{TraceID: "trace-1", TurnID: "turn-1", ToolCallID: "call-1"})
	ctx = WithKBRuntimeContext(ctx, KBRuntimeContext{
		DomainID:   "support",
		OwnerScope: kb.OwnerScopeUser,
		OwnerID:    "user-1",
		SessionID:  "session-1",
	})

	result, err := host.ExecuteTool(ctx, "kb.doc.meta", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteTool(kb.doc.meta): %v", err)
	}
	if !result.IsError {
		t.Fatalf("unbound support domain should be returned as recoverable error")
	}
	if service.resolveInput.DomainID != "support" || service.resolveInput.SessionID != "session-1" {
		t.Fatalf("ResolveBinding input = %+v, want strict support/session-1 without generic fallback", service.resolveInput)
	}
	if strings.Contains(result.DecodeContent(), "generic") {
		t.Fatalf("KB tool error should not mention generic fallback: %s", result.DecodeContent())
	}
}

func TestKBWrapperRejectsMixedNodeIDsAndPageRanges(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	service := &fakeKBService{namespaces: []string{"ns-1"}}
	registerKBTools(host, logger, service)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Status: "active"})
	ctx = toolctx.WithSessionID(ctx, "session-1")
	ctx = toolctx.WithToolContext(ctx, &toolctx.ToolContext{TraceID: "trace-1", TurnID: "turn-1", ToolCallID: "call-1"})
	ctx = WithKBRuntimeContext(ctx, KBRuntimeContext{DomainID: "support", OwnerScope: kb.OwnerScopeUser, OwnerID: "user-1"})

	result, err := host.ExecuteTool(ctx, "kb.section.text", json.RawMessage(`{"doc_id":"doc-1","node_ids":["0001"],"page_ranges":["5"]}`))
	if err != nil {
		t.Fatalf("ExecuteTool(kb.section.text): %v", err)
	}
	if !result.IsError {
		t.Fatalf("mixed node_ids/page_ranges should be rejected")
	}
	if !strings.Contains(result.DecodeContent(), "不能同时提供") {
		t.Fatalf("unexpected mixed input error: %s", result.DecodeContent())
	}
}
