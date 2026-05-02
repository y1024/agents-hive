package acpclient

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPTransportPostsJSONRPCAndStreamsSSEResponse(t *testing.T) {
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		requests <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\n")
		_, _ = io.WriteString(w, `data: {"jsonrpc":"2.0","id":1,"result":{"ok":true}}`+"\n\n")
	}))
	defer server.Close()

	transport, err := newHTTPTransport(server.URL, map[string]string{"Authorization": "Bearer test-token"})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	defer transport.Close()

	if _, err := io.WriteString(transport.Writer, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if got := <-requests; got != `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n" {
		t.Fatalf("posted body = %q", got)
	}

	line, err := bufio.NewReader(transport.Reader).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(line, `"result":{"ok":true}`) {
		t.Fatalf("response line = %q", line)
	}
}
