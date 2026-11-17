package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

type message struct {
	Index   int    `json:"index"`
	Type    int    `json:"type"`
	Status  int    `json:"status"`
	Role    string `json:"role,omitempty"`
	Text    string `json:"text,omitempty"`
	Payload string `json:"payload_base64,omitempty"`
}

// The inspected agy v1.5.9 schema stores serialized Step protos in steps.step_payload.
// Keep unknown steps losslessly rather than guessing their structure.
func field(data []byte, wanted protowire.Number) []byte {
	for len(data) > 0 {
		n, typ, k := protowire.ConsumeTag(data)
		if k < 0 {
			return nil
		}
		data = data[k:]
		if typ == protowire.BytesType {
			v, k := protowire.ConsumeBytes(data)
			if k < 0 {
				return nil
			}
			if n == wanted {
				return v
			}
			data = data[k:]
		} else {
			k := protowire.ConsumeFieldValue(n, typ, data)
			if k < 0 {
				return nil
			}
			data = data[k:]
		}
	}
	return nil
}
func messages(ctx context.Context, path string, since int) ([]message, error) {
	if path == "" {
		return nil, errors.New("conversation unknown; start agy with --conversation=<full-id> or set AGY_TAP_DB")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(1000)")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT idx,step_type,status,step_payload FROM steps WHERE idx > ? ORDER BY idx LIMIT 500", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []message{}
	for rows.Next() {
		var m message
		var payload []byte
		if err = rows.Scan(&m.Index, &m.Type, &m.Status, &payload); err != nil {
			return nil, err
		}
		switch m.Type {
		case 14:
			m.Role = "user"
			m.Text = string(field(field(payload, 19), 2))
		case 15:
			m.Role = "assistant"
			m.Text = string(field(field(payload, 20), 1))
		}
		if m.Text == "" {
			m.Payload = base64.StdEncoding.EncodeToString(payload)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
