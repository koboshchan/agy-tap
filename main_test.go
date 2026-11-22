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
	"strings"
	"testing"
)

func testTap() *tap {
	return &tap{p: new(bytes.Buffer), screen: vt.NewEmulator(80, 24), info: discovery{Token: "secret"}, audit: log.New(io.Discard, "", 0)}
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
