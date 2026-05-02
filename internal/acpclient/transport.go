package acpclient

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// Transport 封装与远程 ACP Agent 的双向通信管道
type Transport struct {
	Reader io.Reader // 从远程 Agent 读取
	Writer io.Writer // 向远程 Agent 写入
	closer func() error
}

// Close 关闭传输连接
func (t *Transport) Close() error {
	if t.closer != nil {
		return t.closer()
	}
	return nil
}

// newStdioTransport 启动 stdio 模式进程并返回 Transport
func newStdioTransport(command string, args []string) (*Transport, error) {
	if command == "" {
		return nil, errs.New(errs.CodeACPClientConnFailed, "stdio 模式需要指定 command")
	}

	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errs.Wrap(errs.CodeACPClientConnFailed, fmt.Sprintf("获取 stdin pipe 失败: %s", command), err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, errs.Wrap(errs.CodeACPClientConnFailed, fmt.Sprintf("获取 stdout pipe 失败: %s", command), err)
	}

	if err := cmd.Start(); err != nil {
		return nil, errs.Wrap(errs.CodeACPClientConnFailed, fmt.Sprintf("启动进程失败: %s", command), err)
	}

	return &Transport{
		Reader: stdout,
		Writer: stdin,
		closer: func() error {
			stdin.Close()
			return cmd.Process.Kill()
		},
	}, nil
}

type httpACPWriter struct {
	ctx     context.Context
	client  *http.Client
	url     string
	headers map[string]string
	out     *io.PipeWriter
	wg      *sync.WaitGroup
}

func (w *httpACPWriter) Write(p []byte) (int, error) {
	payload := append([]byte(nil), p...)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		if err := w.post(payload); err != nil {
			_ = w.out.CloseWithError(err)
		}
	}()
	return len(p), nil
}

func (w *httpACPWriter) post(payload []byte) error {
	req, err := http.NewRequestWithContext(w.ctx, http.MethodPost, w.url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/x-ndjson, text/event-stream")
	for key, value := range w.headers {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("http acp transport status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return copyHTTPACPResponse(w.out, resp)
}

func copyHTTPACPResponse(out io.Writer, resp *http.Response) error {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return copySSEResponse(out, resp.Body)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if _, err := out.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func copySSEResponse(out io.Writer, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if _, err := io.WriteString(out, data+"\n"); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// newHTTPTransport 通过 HTTP POST 发送 line-delimited JSON-RPC，并把 JSON/SSE 响应桥回 SDK reader。
func newHTTPTransport(url string, headers map[string]string) (*Transport, error) {
	if url == "" {
		return nil, errs.New(errs.CodeACPClientConnFailed, "http 模式需要指定 url")
	}

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	writer := &httpACPWriter{
		ctx:     ctx,
		client:  http.DefaultClient,
		url:     url,
		headers: headers,
		out:     pw,
		wg:      &wg,
	}

	return &Transport{
		Reader: pr,
		Writer: writer,
		closer: func() error {
			cancel()
			wg.Wait()
			_ = pw.Close()
			_ = pr.Close()
			return nil
		},
	}, nil
}

// NewTransport 根据配置创建传输连接
func NewTransport(cfg RemoteAgentConfig) (*Transport, error) {
	switch cfg.Transport {
	case "stdio":
		return newStdioTransport(cfg.Command, cfg.Args)
	case "http":
		return newHTTPTransport(cfg.URL, cfg.Headers)
	default:
		return nil, errs.New(errs.CodeACPClientConnFailed,
			fmt.Sprintf("不支持的传输类型: %q（仅支持 stdio 和 http）", cfg.Transport))
	}
}
