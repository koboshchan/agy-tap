package main

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/charmbracelet/x/vt"
	"google.golang.org/protobuf/encoding/protowire"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testTap() *tap {
	return &tap{p: new(bytes.Buffer), screen: vt.NewEmulator(80, 24), prompt: regexp.MustCompile(`(?m)^> *$`), info: discovery{Token: "secret"}, audit: log.New(io.Discard, "", 0)}
}
func TestHTTP(t *testing.T) {
	a := testTap()
	for _, tt := range []struct {
		body, token, origin string
		code                int
	}{
		{`{"text":"hello"}`, "", "", 401},
		{`{"text":"hello"}`, "secret", "https://evil.example", 403},
		{`{"text":"hello\u001b[201~"}`, "secret", "", 400},
		{`{"text":"hello"} {}`, "secret", "", 400},
		{`{"text":"hello"}`, "secret", "", 202},
	} {
		r := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(tt.body))
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != tt.code {
			t.Fatalf("%d != %d: %s", w.Code, tt.code, w.Body.String())
		}
	}
}
func TestQueue(t *testing.T) {
	a := testTap()
	a.screen.WriteString("> ")
	a.queue = []queued{{1, "hello"}}
	a.dirty = true
	a.dispatch()
	if len(a.queue) != 1 {
		t.Fatal("clobbered user input")
	}
	a.dirty = false
	a.dispatch()
	if len(a.queue) != 0 || !a.awaiting {
		t.Fatal("not sent")
	}
	if got := a.p.(*bytes.Buffer).String(); got != "hello\r" {
		t.Fatal(got)
	}
	a.queue = []queued{{2, "again"}}
	a.lastSend = time.Now().Add(-10 * time.Second)
	a.dispatch()
	if len(a.queue) != 1 {
		t.Fatal("sent to stale prompt")
	}
}
func TestMultiline(t *testing.T) {
	a := testTap()
	a.screen.WriteString("> ")
	a.queue = []queued{{1, "a\nb"}}
	a.dispatch()
	if len(a.queue) != 1 {
		t.Fatal("unbracketed paste")
	}
	a.bracketed = true
	a.dispatch()
	if a.p.(*bytes.Buffer).String() != "\x1b[200~a\nb\x1b[201~\r" {
		t.Fatal("bad paste")
	}
}
func wrap(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func TestTranscriptWAL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.db")
	db, e := sql.Open("sqlite", p)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_payload BLOB)")
	if e != nil {
		t.Fatal(e)
	}
	payload := wrap(20, wrap(1, []byte("hello")))
	_, e = db.Exec("INSERT INTO steps VALUES(1,15,3,?)", payload)
	if e != nil {
		t.Fatal(e)
	}
	m, e := messages(context.Background(), p, -1)
	if e != nil || len(m) != 1 || m[0].Text != "hello" {
		t.Fatalf("%+v %v", m, e)
	}
	m, e = messages(context.Background(), p, 1)
	if e != nil || len(m) != 0 {
		t.Fatalf("%+v %v", m, e)
	}
}

func TestTerminalRepliesDoNotPoisonInput(t *testing.T) {
	a := testTap()
	for _, part := range []string{"\x1b[", "1;1R", "\x1b[?1;2c", "\x1b[I", "\x1b[O"} {
		a.track([]byte(part))
	}
	a.lastInput = time.Now().Add(-time.Second)
	a.screen.WriteString("> ")
	a.lastOutput = time.Now().Add(-500 * time.Millisecond)
	if !a.ready() {
		t.Fatal("terminal protocol blocked idle prompt")
	}
	a.track([]byte("/model\r"))
	a.lastInput = time.Now().Add(-time.Second)
	a.unknown = true
	a.screen.WriteString("\r> ")
	a.lastOutput = time.Now().Add(-400 * time.Millisecond)
	if !a.ready() {
		t.Fatal("menu redraw did not recover")
	}
}
func TestHistoricalPromptIsNotIdle(t *testing.T) {
	a := testTap()
	a.screen.WriteString("> \r\n> unfinished")
	a.lastOutput = time.Now().Add(-time.Second)
	if a.ready() {
		t.Fatal("historical prompt accepted")
	}
	b := testTap()
	b.screen.WriteString("> \r\nGenerating... esc to cancel")
	if b.ready() {
		t.Fatal("busy agent accepted")
	}
}
