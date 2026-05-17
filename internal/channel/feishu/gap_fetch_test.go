package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/chef-guo/agents-hive/internal/master"
)

func TestGapFetchRequestValidate(t *testing.T) {
	now := time.Unix(1710000600, 0)

	tests := []struct {
		name    string
		req     GapFetchRequest
		wantErr int
	}{
		{
			name: "missing container id type",
			req: GapFetchRequest{
				ContainerID: "oc_chat_1",
				Window: GapFetchWindow{
					StartTime: now.Add(-time.Minute),
					EndTime:   now,
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "invalid container id type",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDType("thread"),
				ContainerID:     "oc_chat_1",
				Window: GapFetchWindow{
					StartTime: now.Add(-time.Minute),
					EndTime:   now,
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "missing container id",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDTypeChat,
				Window: GapFetchWindow{
					StartTime: now.Add(-time.Minute),
					EndTime:   now,
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "missing start time",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDTypeChat,
				ContainerID:     "oc_chat_1",
				Window: GapFetchWindow{
					EndTime: now,
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "missing end time",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDTypeChat,
				ContainerID:     "oc_chat_1",
				Window: GapFetchWindow{
					StartTime: now.Add(-time.Minute),
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "end before start",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDTypeChat,
				ContainerID:     "oc_chat_1",
				Window: GapFetchWindow{
					StartTime: now,
					EndTime:   now.Add(-time.Minute),
				},
			},
			wantErr: errs.CodeInvalidArgument,
		},
		{
			name: "zero page size falls back to default",
			req: GapFetchRequest{
				ContainerIDType: GapFetchContainerIDTypeChat,
				ContainerID:     "oc_chat_1",
				Window: GapFetchWindow{
					StartTime: now.Add(-time.Minute),
					EndTime:   now,
				},
			},
			wantErr: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == 0 {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() expected error, got nil")
			}
			if !errs.IsCode(err, tt.wantErr) {
				t.Fatalf("Validate() error code = %d, want %d, err=%v", errs.GetCode(err), tt.wantErr, err)
			}
		})
	}
}

func TestGapFetchRequestBuildListMessageReq(t *testing.T) {
	start := time.Unix(1710000000, 0)
	end := start.Add(5 * time.Minute)

	req := GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: start,
			EndTime:   end,
		},
		PageSize:  77,
		PageToken: "next-page",
	}

	params, err := req.Params()
	if err != nil {
		t.Fatalf("Params() error = %v", err)
	}
	if got := params.ContainerIDType; got != "chat" {
		t.Fatalf("container_id_type = %q, want chat", got)
	}
	if got := params.ContainerID; got != "oc_chat_1" {
		t.Fatalf("container_id = %q, want oc_chat_1", got)
	}
	if got := params.SortType; got != string(GapFetchSortByCreateTimeAsc) {
		t.Fatalf("sort_type = %q, want %q", got, GapFetchSortByCreateTimeAsc)
	}
	if got := params.StartTime; got != strconv.FormatInt(start.Unix(), 10) {
		t.Fatalf("start_time = %q, want %d", got, start.Unix())
	}
	if got := params.EndTime; got != strconv.FormatInt(end.Unix(), 10) {
		t.Fatalf("end_time = %q, want %d", got, end.Unix())
	}
	if got := params.PageSize; got != 77 {
		t.Fatalf("page_size = %d, want 77", got)
	}
	if got := params.PageToken; got != "next-page" {
		t.Fatalf("page_token = %q, want next-page", got)
	}
}

func TestListGapMessages_BuildsExpectedRequest(t *testing.T) {
	var capturedQuery url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "tenant_access_token"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"tenant_access_token": "test_token",
				"expire":              7200,
			})
		case r.URL.Path == "/open-apis/im/v1/messages":
			capturedQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"has_more":   false,
					"page_token": "",
					"items": []map[string]any{
						{
							"message_id": "om_1",
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("app_id", "app_secret", zap.NewNop(), lark.WithOpenBaseUrl(server.URL))
	start := time.Unix(1710000000, 0)
	end := start.Add(2 * time.Minute)

	resp, err := client.ListGapMessages(context.Background(), GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: start,
			EndTime:   end,
		},
		PageSize:  20,
		PageToken: "page-2",
	})
	if err != nil {
		t.Fatalf("ListGapMessages() error = %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("Items len = %d, want 1", len(resp.Items))
	}
	if capturedQuery == nil {
		t.Fatal("request query was not captured")
	}
	if got := capturedQuery.Get("container_id_type"); got != "chat" {
		t.Fatalf("container_id_type = %q, want chat", got)
	}
	if got := capturedQuery.Get("container_id"); got != "oc_chat_1" {
		t.Fatalf("container_id = %q, want oc_chat_1", got)
	}
	if got := capturedQuery.Get("start_time"); got != strconv.FormatInt(start.Unix(), 10) {
		t.Fatalf("start_time = %q, want %d", got, start.Unix())
	}
	if got := capturedQuery.Get("end_time"); got != strconv.FormatInt(end.Unix(), 10) {
		t.Fatalf("end_time = %q, want %d", got, end.Unix())
	}
	if got := capturedQuery.Get("page_size"); got != "20" {
		t.Fatalf("page_size = %q, want 20", got)
	}
	if got := capturedQuery.Get("page_token"); got != "page-2" {
		t.Fatalf("page_token = %q, want page-2", got)
	}
	if got := capturedQuery.Get("sort_type"); got != string(GapFetchSortByCreateTimeAsc) {
		t.Fatalf("sort_type = %q, want %q", got, GapFetchSortByCreateTimeAsc)
	}
}

func TestGapFetchWalkerFetchAll_NoResults(t *testing.T) {
	client := &gapFetchTestClient{
		pages: []GapFetchPageResponse{
			{
				Items:   nil,
				HasMore: false,
			},
		},
	}

	walker := NewGapFetchWalker(client)
	pages, err := walker.FetchAll(context.Background(), GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: time.Unix(1710000000, 0),
			EndTime:   time.Unix(1710000300, 0),
		},
	})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages len = %d, want 1", len(pages))
	}
	if len(pages[0].Items) != 0 {
		t.Fatalf("page[0].Items len = %d, want 0", len(pages[0].Items))
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(client.requests))
	}
}

func TestGapFetchRunner_ReplayWindowRoutesThroughRouterAndClaimsByMessageID(t *testing.T) {
	t.Parallel()

	proc := &gapFetchCaptureProcessor{}
	router := channel.NewRouter(proc, zap.NewNop())
	router.RegisterPlugin(&noopPlugin{platform: channel.PlatformFeishu})
	router.Bind(channel.Binding{
		Platform:  channel.PlatformFeishu,
		ChatID:    "oc_chat_1",
		SessionID: "sess-1",
	})

	claimer := master.NewMemoryEventClaimer(0, zap.NewNop())
	router.SetEventClaimer(claimer)

	runner := newGapFetchRunner(router, &gapFetchTestClient{
		pages: []GapFetchPageResponse{
			{
				Items: []GapFetchMessage{
					{
						MessageID: "om_gap_1",
						Raw: larkim.NewMessageBuilder().
							MessageId("om_gap_1").
							ChatId("oc_chat_1").
							MsgType("text").
							CreateTime(strconv.FormatInt(time.Unix(1710000000, 0).UnixMilli(), 10)).
							Sender(larkim.NewSenderBuilder().Id("ou_sender_1").Build()).
							Body(larkim.NewMessageBodyBuilder().Content(`{"text":"hello"}`).Build()).
							Build(),
					},
					{
						MessageID: "om_gap_2",
						Raw: larkim.NewMessageBuilder().
							MessageId("om_gap_2").
							ChatId("oc_chat_1").
							MsgType("text").
							CreateTime(strconv.FormatInt(time.Unix(1710000005, 0).UnixMilli(), 10)).
							Sender(larkim.NewSenderBuilder().Id("ou_sender_1").Build()).
							Body(larkim.NewMessageBodyBuilder().Content(`{"text":"world"}`).Build()).
							Build(),
					},
				},
			},
		},
	}, zap.NewNop())

	err := runner.ReplayWindow(context.Background(), "tenant-a", GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: time.Unix(1710000000, 0),
			EndTime:   time.Unix(1710000300, 0),
		},
	})
	if err != nil {
		t.Fatalf("ReplayWindow() error = %v", err)
	}

	got := proc.waitCalls(t, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("processor calls = %d, want 2", len(got))
	}
	if got[0].messageID != "om_gap_1" {
		t.Fatalf("channel message id = %q, want om_gap_1", got[0].messageID)
	}
	if got[0].content != "hello" {
		t.Fatalf("content = %q, want hello", got[0].content)
	}
	if got[1].messageID != "om_gap_2" {
		t.Fatalf("second channel message id = %q, want om_gap_2", got[1].messageID)
	}
	if got[1].content != "world" {
		t.Fatalf("second content = %q, want world", got[1].content)
	}
	if state := claimer.State("feishu_gap_fetch:om_gap_1"); state != master.ClaimStateCompleted {
		t.Fatalf("claimer state = %d, want completed", state)
	}
	if state := claimer.State("feishu_gap_fetch:om_gap_2"); state != master.ClaimStateCompleted {
		t.Fatalf("second claimer state = %d, want completed", state)
	}
}

func TestGapFetchRunner_ReplayWindowSkipsDuplicateSyntheticClaim(t *testing.T) {
	t.Parallel()

	proc := &gapFetchCaptureProcessor{}
	router := channel.NewRouter(proc, zap.NewNop())
	router.RegisterPlugin(&noopPlugin{platform: channel.PlatformFeishu})
	router.Bind(channel.Binding{
		Platform:  channel.PlatformFeishu,
		ChatID:    "oc_chat_1",
		SessionID: "sess-1",
	})

	claimer := master.NewMemoryEventClaimer(0, zap.NewNop())
	router.SetEventClaimer(claimer)
	_, _ = claimer.ClaimEvent("feishu_gap_fetch:om_gap_dup", master.DefaultClaimLease)
	_ = claimer.CompleteEvent(master.ClaimToken{EventID: "feishu_gap_fetch:om_gap_dup"})

	runner := newGapFetchRunner(router, &gapFetchTestClient{
		pages: []GapFetchPageResponse{
			{
				Items: []GapFetchMessage{{
					MessageID: "om_gap_dup",
					Raw: larkim.NewMessageBuilder().
						MessageId("om_gap_dup").
						ChatId("oc_chat_1").
						MsgType("text").
						Body(larkim.NewMessageBodyBuilder().Content(`{"text":"dup"}`).Build()).
						Build(),
				}},
			},
		},
	}, zap.NewNop())

	err := runner.ReplayWindow(context.Background(), "tenant-a", GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: time.Unix(1710000000, 0),
			EndTime:   time.Unix(1710000300, 0),
		},
	})
	if err != nil {
		t.Fatalf("ReplayWindow() error = %v", err)
	}

	if got := proc.callsLen(); got != 0 {
		t.Fatalf("processor calls = %d, want 0", got)
	}
}

func TestGapFetchWalkerFetchAll_AdvancesPagination(t *testing.T) {
	client := &gapFetchTestClient{
		pages: []GapFetchPageResponse{
			{
				Items:         []GapFetchMessage{{MessageID: "om_1"}},
				HasMore:       true,
				NextPageToken: "page-2",
			},
			{
				Items:   []GapFetchMessage{{MessageID: "om_2"}},
				HasMore: false,
			},
		},
	}

	walker := NewGapFetchWalker(client)
	pages, err := walker.FetchAll(context.Background(), GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: time.Unix(1710000000, 0),
			EndTime:   time.Unix(1710000300, 0),
		},
		PageSize: 50,
	})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages len = %d, want 2", len(pages))
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests len = %d, want 2", len(client.requests))
	}
	if client.requests[0].PageToken != "" {
		t.Fatalf("first PageToken = %q, want empty", client.requests[0].PageToken)
	}
	if client.requests[1].PageToken != "page-2" {
		t.Fatalf("second PageToken = %q, want page-2", client.requests[1].PageToken)
	}
}

func TestGapFetchWalkerFetchAll_HasMoreWithoutNextTokenReturnsError(t *testing.T) {
	client := &gapFetchTestClient{
		pages: []GapFetchPageResponse{
			{
				Items:   []GapFetchMessage{{MessageID: "om_1"}},
				HasMore: true,
			},
		},
	}

	walker := NewGapFetchWalker(client)
	_, err := walker.FetchAll(context.Background(), GapFetchRequest{
		ContainerIDType: GapFetchContainerIDTypeChat,
		ContainerID:     "oc_chat_1",
		Window: GapFetchWindow{
			StartTime: time.Unix(1710000000, 0),
			EndTime:   time.Unix(1710000300, 0),
		},
	})
	if err == nil {
		t.Fatal("FetchAll() expected error, got nil")
	}
	if !errs.IsCode(err, errs.CodeChannelSendFailed) {
		t.Fatalf("error code = %d, want %d, err=%v", errs.GetCode(err), errs.CodeChannelSendFailed, err)
	}
}

type gapFetchTestClient struct {
	pages    []GapFetchPageResponse
	requests []GapFetchRequest
}

func (c *gapFetchTestClient) ListGapMessages(ctx context.Context, req GapFetchRequest) (GapFetchPageResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.pages) == 0 {
		return GapFetchPageResponse{}, nil
	}
	page := c.pages[0]
	c.pages = c.pages[1:]
	return page, nil
}

type gapFetchFailingClient struct{}

func (c *gapFetchFailingClient) ListGapMessages(ctx context.Context, req GapFetchRequest) (GapFetchPageResponse, error) {
	return GapFetchPageResponse{}, errs.New(errs.CodeChannelSendFailed, "gap fetch forced failure")
}

type gapFetchMultiChatClient struct {
	mu          sync.Mutex
	pagesByChat map[string][]GapFetchPageResponse
	errByChat   map[string]error
	requests    []GapFetchRequest
}

func (c *gapFetchMultiChatClient) ListGapMessages(ctx context.Context, req GapFetchRequest) (GapFetchPageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests = append(c.requests, req)
	if err := c.errByChat[req.ContainerID]; err != nil {
		return GapFetchPageResponse{}, err
	}
	pages := c.pagesByChat[req.ContainerID]
	if len(pages) == 0 {
		return GapFetchPageResponse{}, nil
	}
	page := pages[0]
	c.pagesByChat[req.ContainerID] = pages[1:]
	return page, nil
}

type gapFetchCaptureCall struct {
	messageID string
	content   string
}

type gapFetchCaptureProcessor struct {
	mu    sync.Mutex
	calls []gapFetchCaptureCall
}

func (p *gapFetchCaptureProcessor) ProcessMessage(_ context.Context, _ string, input string) (master.TaskResponse, error) {
	return master.TaskResponse{Content: "ok"}, nil
}

func (p *gapFetchCaptureProcessor) ProcessMessageFromIM(_ context.Context, _ string, input, channelMessageID string, _ []master.FileAttachment, _ string, _ bool, imCtx *imctx.IMMessageContext) (master.TaskResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, gapFetchCaptureCall{
		messageID: channelMessageID,
		content:   input,
	})
	p.mu.Unlock()
	return master.TaskResponse{Content: "ok"}, nil
}

func (p *gapFetchCaptureProcessor) waitCalls(t *testing.T, want int, timeout time.Duration) []gapFetchCaptureCall {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		if len(p.calls) >= want {
			out := append([]gapFetchCaptureCall(nil), p.calls...)
			p.mu.Unlock()
			return out
		}
		p.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]gapFetchCaptureCall(nil), p.calls...)
}

func (p *gapFetchCaptureProcessor) callsLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}
