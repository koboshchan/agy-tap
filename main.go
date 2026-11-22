package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	"golang.org/x/term"
)

type discovery struct {
	PID          int    `json:"pid"`
	Port         int    `json:"port"`
	Token        string `json:"token,omitempty"`
	Conversation string `json:"conversation"`
	DB           string `json:"db"`
}
type request struct {
	Text string `json:"text"`
}
type tap struct {
	mu        sync.Mutex
	p         io.Writer
	screen    *vt.Emulator
	bracketed bool
	next      int
	info      discovery
	audit     *log.Logger
}

func stateDir() string {
	if p := os.Getenv("AGY_TAP_DIR"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".agy-tap")
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("agy-tap 0.1.4")
		return nil
	}
	if len(args) > 0 && args[0] != "--" {
		return client(args)
	}
	if len(args) < 2 {
		return errors.New("usage: agy-tap -- agy --conversation=<id> | agy-tap list | agy-tap send <pid> [--] <text> | agy-tap force <pid> | agy-tap messages <pid> [since]")
	}
	args = args[1:]
	conv := ""
	for i, a := range args {
		if strings.HasPrefix(a, "--conversation=") {
			conv = strings.TrimPrefix(a, "--conversation=")
		}
		if a == "--conversation" && i+1 < len(args) {
			conv = args[i+1]
		}
	}
	if conv != "" && !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(conv) {
		return errors.New("invalid conversation ID")
	}
	h, _ := os.UserHomeDir()
	db := os.Getenv("AGY_TAP_DB")
	if db == "" && conv != "" {
		db = filepath.Join(h, ".gemini", "antigravity-cli", "conversations", conv+".db")
	}
	dir := stateDir()
	if err := privateDir(dir); err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	info := discovery{os.Getpid(), ln.Addr().(*net.TCPAddr).Port, hex.EncodeToString(token), conv, db}
	file := filepath.Join(dir, strconv.Itoa(info.PID)+".json")
	raw, _ := json.Marshal(info)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		return err
	}
	defer os.Remove(file)
	auditFile, err := os.OpenFile(filepath.Join(dir, strconv.Itoa(info.PID)+".injections.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer auditFile.Close()
	w, ht := 120, 40
	if x, y, e := terminalSize(); e == nil {
		w, ht = x, y
	}
	p, err := xpty.NewPty(w, ht)
	if err != nil {
		return err
	}
	defer p.Close()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	prepareChildTerminal(cmd)
	t := &tap{p: p, screen: vt.NewEmulator(w, ht), info: info, audit: log.New(auditFile, "", 0)}
	t.screen.SetCallbacks(vt.Callbacks{EnableMode: func(m ansi.Mode) {
		if m.Mode() == 2004 {
			t.bracketed = true
		}
	}, DisableMode: func(m ansi.Mode) {
		if m.Mode() == 2004 {
			t.bracketed = false
		}
	}})
	// Drain emulator responses, but don't feed them back: the real terminal answers queries.
	go io.Copy(io.Discard, t.screen)
	// Keep wrapper diagnostics outside the child's cursor coordinate stream.
	fmt.Fprintf(os.Stderr, "agy-tap control file %s\r\n", file)
	restoreOutput, err := prepareOutput()
	if err != nil {
		return err
	}
	defer restoreOutput()
	if err = p.Start(cmd); err != nil {
		return err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	var old *term.State
	if term.IsTerminal(int(os.Stdin.Fd())) {
		old, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &http.Server{Handler: t.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	go server.Serve(ln)
	defer server.Close()
	go func() {
		buf := make([]byte, 32768)
		for {
			n, e := p.Read(buf)
			if n > 0 {
				t.mu.Lock()
				_, _ = t.screen.Write(buf[:n])
				t.mu.Unlock()
				_, _ = os.Stdout.Write(buf[:n])
			}
			if e != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, e := os.Stdin.Read(buf)
			if n > 0 {
				if t.writeInput(buf[:n]) != nil {
					return
				}
			}
			if e != nil {
				return
			}
		}
	}()
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				x, y, e := terminalSize()
				if e == nil && x > 0 && y > 0 && (x != w || y != ht) {
					t.mu.Lock()
					_ = p.Resize(x, y)
					t.screen.Resize(x, y)
					t.mu.Unlock()
					w, ht = x, y
				}
			}
		}
	}()
	return xpty.WaitProcess(ctx, cmd)
}

// writeInput serializes each physical-input read with complete control submissions.
// It deliberately does not track or preserve the application's editor/draft state.
func (t *tap) writeInput(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.write(data)
}

// write requires mu. Never retry a failed/short write: it may have submitted input.
func (t *tap) write(data []byte) error {
	n, err := t.p.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

// inject requires mu and uses agy's ordinary paste + Enter input path.
func (t *tap) inject(text string) error {
	payload := text
	if t.bracketed {
		payload = "\x1b[200~" + payload + "\x1b[201~"
	}
	return t.controlWrite("send", text, []byte(payload+"\r"))
}

// controlWrite requires mu. Only explicitly supplied text is audited, never local input.
func (t *tap) controlWrite(kind, text string, data []byte) error {
	t.next++
	event := map[string]any{"id": t.next, "time": time.Now().UTC(), "command": kind, "event": "attempt"}
	if kind == "send" {
		event["text"] = text
	}
	b, _ := json.Marshal(event)
	t.audit.Print(string(b))
	err := t.write(data)
	event["event"] = "injected"
	if err != nil {
		event["event"], event["error"] = "write_error", err.Error()
	}
	b, _ = json.Marshal(event)
	t.audit.Print(string(b))
	return err
}

func (t *tap) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		t.mu.Lock()
		defer t.mu.Unlock()
		reply(w, map[string]any{"pid": t.info.PID, "conversation": t.info.Conversation, "bracketed_paste": t.bracketed})
	})
	mux.HandleFunc("GET /screen", func(w http.ResponseWriter, r *http.Request) {
		t.mu.Lock()
		defer t.mu.Unlock()
		reply(w, map[string]any{"screen": t.screen.String(), "width": t.screen.Width(), "height": t.screen.Height()})
	})
	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		since := -1
		var err error
		if s := r.URL.Query().Get("since"); s != "" {
			since, err = strconv.Atoi(s)
			if err != nil {
				http.Error(w, "invalid since", 400)
				return
			}
		}
		m, err := messages(r.Context(), t.info.DB, since)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		reply(w, m)
	})
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		var q request
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(&q); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			http.Error(w, "trailing JSON", 400)
			return
		}
		if err := validText(q.Text); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if strings.Contains(q.Text, "\n") && !t.bracketed {
			http.Error(w, "multiline text requires negotiated bracketed paste", http.StatusConflict)
			return
		}
		if err := t.inject(q.Text); err != nil {
			http.Error(w, "terminal write failed; input may be partially submitted, do not blindly retry", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		reply(w, map[string]any{"id": t.next, "injected": true})
	})
	mux.HandleFunc("POST /force", func(w http.ResponseWriter, r *http.Request) {
		// Trigger only. Reject payloads rather than silently interpreting them as text.
		r.Body = http.MaxBytesReader(w, r.Body, 1)
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			http.Error(w, "force accepts no body", http.StatusBadRequest)
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		// Explicit native Alt+M shortcut, not a cancellation or a tap queue bypass.
		if err := t.controlWrite("force", "", []byte("\x1bm")); err != nil {
			http.Error(w, "terminal write failed; shortcut may be partially sent, do not blindly retry", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		reply(w, map[string]any{"id": t.next, "injected": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins forbidden", 403)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+t.info.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func validText(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("empty text")
	}
	for _, c := range s {
		if c < 32 && c != '\n' && c != '\t' {
			return errors.New("control characters forbidden")
		}
		if c == 127 {
			return errors.New("control characters forbidden")
		}
	}
	return nil
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func client(args []string) error {
	if len(args) == 0 {
		return errors.New("expected command")
	}
	var send request
	if args[0] == "send" {
		pid, q, err := parseSend(args[1:])
		if err != nil {
			return err
		}
		send = q
		args = []string{"send", pid}
	}
	if args[0] == "force" && len(args) != 2 {
		return errors.New("expected force PID (no text or flags)")
	}

	files, _ := filepath.Glob(filepath.Join(stateDir(), "*.json"))
	if args[0] == "list" {
		for _, p := range files {
			var d discovery
			b, e := os.ReadFile(p)
			if e == nil && json.Unmarshal(b, &d) == nil {
				if !alive(d) {
					if !processExists(d.PID) {
						_ = os.Remove(p)
					}
					continue
				}
				d.Token = ""
				b, _ = json.Marshal(d)
				fmt.Println(string(b))
			}
		}
		return nil
	}
	if len(args) < 2 {
		return errors.New("expected command and PID")
	}
	pid, err := strconv.Atoi(args[1])
	if err != nil || pid <= 0 {
		return errors.New("invalid PID")
	}
	b, err := os.ReadFile(filepath.Join(stateDir(), strconv.Itoa(pid)+".json"))
	if err != nil {
		return err
	}
	var d discovery
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	if args[0] == "messages" {
		since := -1
		if len(args) > 2 {
			since, err = strconv.Atoi(args[2])
			if err != nil {
				return err
			}
		}
		m, e := messages(context.Background(), d.DB, since)
		if e != nil {
			return e
		}
		for _, v := range m {
			if v.Role != "" && v.Text != "" {
				fmt.Printf("[%d] %s\n%s\n\n", v.Index, v.Role, v.Text)
			}
		}
		return nil
	}
	endpoint, method, body := "/"+args[0], "GET", []byte(nil)
	switch args[0] {
	case "send":
		body, _ = json.Marshal(send)
		method = "POST"
	case "force":
		method = "POST"
	case "messages":
		if len(args) > 2 {
			n, e := strconv.Atoi(args[2])
			if e != nil {
				return e
			}
			endpoint += "?since=" + strconv.Itoa(n)
		}
	case "status", "screen":
	default:
		return errors.New("unknown command")
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", d.Port, endpoint), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+d.Token)
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(os.Stdout, res.Body)
	if res.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return err
}

// No send flags. An optional -- allows text beginning with dashes; once text
// starts, every subsequent argument is literal and is never interpreted as a flag.
func parseSend(args []string) (string, request, error) {
	var q request
	if len(args) < 2 {
		return "", q, errors.New("expected send PID [--] text")
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil || pid <= 0 {
		return "", q, errors.New("invalid PID")
	}
	text := args[1:]
	if text[0] == "--" {
		text = text[1:]
	} else if strings.HasPrefix(text[0], "-") {
		return "", q, errors.New("send has no flags; use -- before literal leading dashes")
	}
	q.Text = strings.Join(text, " ")
	if err := validText(q.Text); err != nil {
		return "", q, err
	}
	return strconv.Itoa(pid), q, nil
}

// Probe the authenticated endpoint rather than trusting reused OS process IDs.
func alive(d discovery) bool {
	c := &http.Client{Timeout: 750 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	r, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/status", d.Port), nil)
	r.Header.Set("Authorization", "Bearer "+d.Token)
	res, e := c.Do(r)
	if e != nil {
		return false
	}
	defer res.Body.Close()
	var v struct{ PID int }
	return res.StatusCode == 200 && json.NewDecoder(res.Body).Decode(&v) == nil && v.PID == d.PID
}
