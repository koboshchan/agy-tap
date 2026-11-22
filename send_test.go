package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func control(a *tap, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}
func TestImmediateSendNoQueueOrReadiness(t *testing.T) {
	a := testTap()
	a.screen.WriteString("Generating, not a prompt")
	for i := 0; i < 70; i++ {
		b, _ := json.Marshal(request{Text: fmt.Sprintf("message-%d 世界", i)})
		w := control(a, "/send", string(b))
		if w.Code != 202 || !strings.Contains(w.Body.String(), `"injected":true`) || strings.Contains(w.Body.String(), "queued") {
			t.Fatal(w.Code, w.Body)
		}
	}
	if strings.Count(a.p.(*bytes.Buffer).String(), "\r") != 70 {
		t.Fatal("send waited or retained a queue")
	}
	r := httptest.NewRequest("GET", "/status", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	for _, obsolete := range []string{"queued", "ready", "input_dirty", "input_unknown"} {
		if strings.Contains(w.Body.String(), obsolete) {
			t.Fatal(w.Body)
		}
	}
}
func TestMultilineAndNativeForce(t *testing.T) {
	a := testTap()
	if w := control(a, "/send", `{"text":"a\n世界"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if a.p.(*bytes.Buffer).Len() != 0 {
		t.Fatal("unsafe multiline written")
	}
	a.bracketed = true
	if w := control(a, "/send", `{"text":"a\n世界\tend"}`); w.Code != 202 {
		t.Fatal(w.Code)
	}
	if got := a.p.(*bytes.Buffer).String(); got != "\x1b[200~a\n世界\tend\x1b[201~\r" {
		t.Fatal(got)
	}
	a.p.(*bytes.Buffer).Reset()
	if w := control(a, "/force", ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if got := a.p.(*bytes.Buffer).String(); got != "\x1bm" {
		t.Fatalf("%q", got)
	}
	for _, body := range []string{" ", "{}", `{"text":"no"}`, "x", strings.Repeat("x", 70000)} {
		before := a.p.(*bytes.Buffer).Len()
		if w := control(a, "/force", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
		if a.p.(*bytes.Buffer).Len() != before {
			t.Fatal("force accepted a body")
		}
	}
}
func TestInputSharedNotDraftProtected(t *testing.T) {
	a := testTap()
	var audit bytes.Buffer
	a.audit.SetOutput(&audit)
	// No draft guard, reconstruction, erase, or secretly logged local text.
	for _, fragment := range []string{"private draft 世界", "\x1b[", "D", "\x1b[200~", "multi\nline", "\x1b[201~"} {
		if err := a.writeInput([]byte(fragment)); err != nil {
			t.Fatal(err)
		}
	}
	before := a.p.(*bytes.Buffer).String()
	if w := control(a, "/send", `{"text":"remote"}`); w.Code != 202 {
		t.Fatal(w.Code)
	}
	if got := a.p.(*bytes.Buffer).String(); got != before+"remote\r" {
		t.Fatalf("%q", got)
	}
	if strings.Contains(audit.String(), "private") || strings.Contains(audit.String(), "multi") {
		t.Fatal("local input leaked into audit")
	}
}
func TestProtocolSecurity(t *testing.T) {
	for _, path := range []string{"/send", "/force"} {
		for _, auth := range []bool{false, true} {
			a := testTap()
			r := httptest.NewRequest("POST", path, nil)
			want := 401
			if auth {
				r.Header.Set("Authorization", "Bearer secret")
				r.Header.Set("Origin", "https://example.com")
				want = 403
			}
			w := httptest.NewRecorder()
			a.handler().ServeHTTP(w, r)
			if w.Code != want || a.p.(*bytes.Buffer).Len() != 0 {
				t.Fatal(w.Code)
			}
		}
	}
	a := testTap()
	for _, body := range []string{
		`{"text":"\u001bm"}`, `{"text":"\u0003"}`, `{"text":"\r"}`, `{"text":"\u007f"}`,
		`{"text":"x","force":true}`, `{"text":"x"} {}`, `{"text":"x","unexpected":true}`,
		`{"text":"x"`, strings.Repeat("x", 70000),
	} {
		if w := control(a, "/send", body); w.Code != 400 {
			t.Fatal(body[:min(len(body), 100)], w.Code)
		}
	}
	if a.p.(*bytes.Buffer).Len() != 0 {
		t.Fatal("invalid input written")
	}
}

type shortWriter struct{ calls int }

func (w *shortWriter) Write(p []byte) (int, error) { w.calls++; return len(p) - 1, nil }
func TestShortWriteNotRetried(t *testing.T) {
	for _, path := range []string{"/send", "/force"} {
		a := testTap()
		w := &shortWriter{}
		a.p = w
		body := ""
		if path == "/send" {
			body = `{"text":"hello"}`
		}
		if r := control(a, path, body); r.Code != 502 {
			t.Fatal(r.Code)
		}
		if w.calls != 1 {
			t.Fatal("retried ambiguous write")
		}
	}
}

type checkedWriter struct {
	mu              sync.Mutex
	active, overlap bool
	frames          []string
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.active {
		w.overlap = true
	}
	w.active = true
	w.mu.Unlock()
	time.Sleep(time.Millisecond)
	w.mu.Lock()
	w.frames = append(w.frames, string(p))
	w.active = false
	w.mu.Unlock()
	return len(p), nil
}
func TestConcurrentSendForcePhysicalWriteSerialization(t *testing.T) {
	a := testTap()
	w := &checkedWriter{}
	a.p = w
	a.bracketed = true
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(request{Text: fmt.Sprintf("message-%d 世界\nnext", i)})
			if r := control(a, "/send", string(b)); r.Code != 202 {
				t.Error(r.Code)
			}
		}(i)
		go func() {
			defer wg.Done()
			if r := control(a, "/force", ""); r.Code != 202 {
				t.Error(r.Code)
			}
		}()
		go func() {
			defer wg.Done()
			if err := a.writeInput([]byte("local世界")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if w.overlap || len(w.frames) != 60 {
		t.Fatal(w.overlap, len(w.frames))
	}
	sends, forces, locals := 0, 0, 0
	for _, f := range w.frames {
		switch {
		case f == "\x1bm":
			forces++
		case f == "local世界":
			locals++
		case strings.HasPrefix(f, "\x1b[200~message-") && strings.HasSuffix(f, "世界\nnext\x1b[201~\r"):
			sends++
		default:
			t.Fatalf("interleaved %q", f)
		}
	}
	if sends != 20 || forces != 20 || locals != 20 {
		t.Fatal(sends, forces, locals)
	}
}
func TestParseSendAndForceArguments(t *testing.T) {
	for _, tt := range []struct {
		args []string
		text string
	}{
		{[]string{"123", "hello", "--force"}, "hello --force"},
		{[]string{"123", "--", "--force"}, "--force"},
		{[]string{"123", "世界\nline"}, "世界\nline"},
	} {
		pid, q, err := parseSend(tt.args)
		if err != nil || pid != "123" || q.Text != tt.text {
			t.Fatal(pid, q, err)
		}
	}
	for _, args := range [][]string{{}, {"123"}, {"123", "--"}, {"--force", "123", "x"}, {"123", "--force", "x"}, {"123", "\x03"}, {"-1", "x"}} {
		if _, _, err := parseSend(args); err == nil {
			t.Fatal(args)
		}
	}
	for _, args := range [][]string{{"force"}, {"force", "123", "text"}, {"force", "--force", "123"}, {"force", "123", "--"}} {
		if err := client(args); err == nil {
			t.Fatal(args)
		}
	}
}
func TestClientSendAndTriggerProtocol(t *testing.T) {
	var paths, bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("protocol changed")
		}
		b, _ := io.ReadAll(r.Body)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, string(b))
		w.WriteHeader(202)
	}))
	defer server.Close()
	var port int
	fmt.Sscanf(strings.TrimPrefix(server.URL, "http://127.0.0.1:"), "%d", &port)
	dir := t.TempDir()
	t.Setenv("AGY_TAP_DIR", dir)
	b, _ := json.Marshal(discovery{PID: 123, Port: port, Token: "secret"})
	if err := os.WriteFile(filepath.Join(dir, "123.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := client([]string{"send", "123", "--", "--force literal 世界"}); err != nil {
		t.Fatal(err)
	}
	if err := client([]string{"force", "123"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/send" || paths[1] != "/force" || bodies[1] != "" {
		t.Fatal(paths, bodies)
	}
	var q request
	if err := json.Unmarshal([]byte(bodies[0]), &q); err != nil || q.Text != "--force literal 世界" {
		t.Fatal(q, err)
	}
}
