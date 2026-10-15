package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/state"
)

type request struct {
	Path        string
	ContentType string
	Body        string
}

type recorder struct {
	mu       sync.Mutex
	requests []request
	status   int
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.requests = append(r.requests, request{Path: req.URL.Path, ContentType: req.Header.Get("Content-Type"), Body: string(body)})
	status := r.status
	r.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (r *recorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.requests))
	for _, req := range r.requests {
		out = append(out, req.Path)
	}
	return out
}

func newServer(t *testing.T) (*recorder, *httptest.Server) {
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(server.Close)
	return rec, server
}

func newNotifier(cfg config.Notify, token string, logs *bytes.Buffer) *Notifier {
	n := New(cfg, token, slog.New(slog.NewTextHandler(logs, nil)))
	n.RetryDelay = time.Millisecond
	return n
}

func sampleRun(status string) state.Run {
	run := state.Run{
		Host:       "vm-app-01",
		Status:     status,
		StartedAt:  time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 10, 15, 19, 2, 13, 500, time.UTC),
		Databases: []state.Database{
			{Source: "main_pg", Name: "app_prod", Status: state.StatusSuccess, SizeBytes: 104857600, RemotePath: "/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump", Duration: time.Second},
		},
	}
	if status != state.StatusSuccess {
		run.Databases = append(run.Databases, state.Database{Source: "shop_mysql", Name: "app_audit", Status: state.StatusFailure, Error: "pg_dump: permission denied for table audit_log"})
	}
	return run
}

func TestWebhookPayload(t *testing.T) {
	rec, server := newServer(t)
	var logs bytes.Buffer
	n := newNotifier(config.Notify{When: config.NotifyFailure, WebhookURL: server.URL + "/hook"}, "", &logs)
	n.Finish(context.Background(), sampleRun(state.StatusPartial))
	if len(rec.requests) != 1 || rec.requests[0].ContentType != "application/json" {
		t.Fatalf("got %+v", rec.requests)
	}
	want := `{"tool":"dbak","host":"vm-app-01","status":"partial","started_at":"2026-10-15T19:00:00Z","finished_at":"2026-10-15T19:02:13Z","databases":[` +
		`{"source":"main_pg","name":"app_prod","status":"success","size_bytes":104857600,"remote_path":"/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump"},` +
		`{"source":"shop_mysql","name":"app_audit","status":"failure","error":"pg_dump: permission denied for table audit_log"}]}`
	if rec.requests[0].Body != want {
		t.Fatalf("got body\n%s\nwant\n%s", rec.requests[0].Body, want)
	}
	if !strings.Contains(logs.String(), "The webhook notification was sent.") {
		t.Fatalf("got logs:\n%s", logs.String())
	}
}

func TestWhen(t *testing.T) {
	cases := []struct {
		when, status string
		sent         bool
	}{
		{config.NotifyFailure, state.StatusSuccess, false},
		{config.NotifyFailure, state.StatusPartial, true},
		{config.NotifyFailure, state.StatusFailure, true},
		{config.NotifyAlways, state.StatusSuccess, true},
		{config.NotifyNever, state.StatusFailure, false},
	}
	for _, c := range cases {
		t.Run(c.when+" and "+c.status, func(t *testing.T) {
			rec, server := newServer(t)
			var logs bytes.Buffer
			n := newNotifier(config.Notify{When: c.when, WebhookURL: server.URL}, "", &logs)
			n.Finish(context.Background(), sampleRun(c.status))
			if got := len(rec.requests) == 1; got != c.sent {
				t.Fatalf("got %d requests", len(rec.requests))
			}
		})
	}
}

func TestTelegram(t *testing.T) {
	rec, server := newServer(t)
	var logs bytes.Buffer
	n := newNotifier(config.Notify{When: config.NotifyAlways, TelegramChatID: "-1001"}, "123:SECRET", &logs)
	n.TelegramAPI = server.URL
	n.Finish(context.Background(), sampleRun(state.StatusSuccess))
	if len(rec.requests) != 1 || rec.requests[0].Path != "/bot123:SECRET/sendMessage" {
		t.Fatalf("got %+v", rec.requests)
	}
	var msg map[string]string
	if err := json.Unmarshal([]byte(rec.requests[0].Body), &msg); err != nil {
		t.Fatal(err)
	}
	if msg["chat_id"] != "-1001" || !strings.Contains(msg["text"], "main_pg/app_prod: success, 100.0 MiB") {
		t.Fatalf("got %v", msg)
	}
}

func TestTelegramText(t *testing.T) {
	run := sampleRun(state.StatusPartial)
	run.Databases[1].Error = strings.Repeat("e", 300)
	text := TelegramText(run)
	want := "dbak on vm-app-01: The backup of some databases failed.\nStarted: 2026-10-15 19:00:00 UTC\n\nmain_pg/app_prod: success, 100.0 MiB\nshop_mysql/app_audit: failure. " + strings.Repeat("e", 200) + "..."
	if text != want {
		t.Fatalf("got\n%s\nwant\n%s", text, want)
	}
}

func TestHealthcheck(t *testing.T) {
	cases := map[string]string{
		state.StatusSuccess: "/ping/abc",
		state.StatusPartial: "/ping/abc/fail",
		state.StatusFailure: "/ping/abc/fail",
	}
	for status, want := range cases {
		t.Run(status, func(t *testing.T) {
			rec, server := newServer(t)
			var logs bytes.Buffer
			n := newNotifier(config.Notify{When: config.NotifyNever, HealthcheckURL: server.URL + "/ping/abc"}, "", &logs)
			n.Start(context.Background())
			n.Finish(context.Background(), sampleRun(status))
			got := rec.paths()
			if len(got) != 2 || got[0] != "/ping/abc/start" || got[1] != want {
				t.Fatalf("got %v, want start and %s", got, want)
			}
		})
	}
}

func TestRetries(t *testing.T) {
	cases := []struct {
		status   int
		attempts int
	}{
		{http.StatusInternalServerError, 3},
		{http.StatusTooManyRequests, 3},
		{http.StatusBadRequest, 1},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			rec, server := newServer(t)
			rec.status = c.status
			var logs bytes.Buffer
			n := newNotifier(config.Notify{When: config.NotifyAlways, WebhookURL: server.URL}, "", &logs)
			n.Finish(context.Background(), sampleRun(state.StatusSuccess))
			if len(rec.requests) != c.attempts {
				t.Fatalf("got %d attempts, want %d", len(rec.requests), c.attempts)
			}
			if !strings.Contains(logs.String(), "The webhook notification failed. The backup result does not change.") {
				t.Fatalf("got logs:\n%s", logs.String())
			}
		})
	}
}

func TestErrorsHideSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	var logs bytes.Buffer
	n := newNotifier(config.Notify{When: config.NotifyAlways, WebhookURL: address + "/hook/SECRETPATH", TelegramChatID: "-1001"}, "123:SECRETTOKEN", &logs)
	n.TelegramAPI = address
	n.Finish(context.Background(), sampleRun(state.StatusFailure))
	out := logs.String()
	if !strings.Contains(out, "The Telegram notification failed.") || !strings.Contains(out, "The webhook notification failed.") {
		t.Fatalf("got logs:\n%s", out)
	}
	for _, secret := range []string{"SECRETPATH", "SECRETTOKEN"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the log contains %s:\n%s", secret, out)
		}
	}
}

func TestTelegramWithoutToken(t *testing.T) {
	var logs bytes.Buffer
	n := newNotifier(config.Notify{TelegramChatID: "-1001"}, "", &logs)
	if err := n.SendTelegram(context.Background(), "x"); err == nil {
		t.Fatal("got no error for an empty token")
	}
}

func TestTelegramErrorDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer server.Close()
	var logs bytes.Buffer
	n := newNotifier(config.Notify{TelegramChatID: "-1001"}, "123:SECRET", &logs)
	n.TelegramAPI = server.URL
	err := n.SendTelegram(context.Background(), "x")
	if err == nil || err.Error() != "the server returned HTTP status 400: Bad Request: chat not found" {
		t.Fatalf("got %v", err)
	}
}

func TestDatabaseLabel(t *testing.T) {
	cases := []struct {
		name string
		db   state.Database
		want string
	}{
		{"with a source", state.Database{Source: "main_pg", Name: "app_prod"}, "main_pg/app_prod"},
		{"from an old run without a source", state.Database{Name: "app_prod"}, "app_prod"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DatabaseLabel(c.db); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNotifierTest(t *testing.T) {
	rec, server := newServer(t)
	var logs bytes.Buffer
	n := newNotifier(config.Notify{When: config.NotifyNever, WebhookURL: server.URL + "/hook", TelegramChatID: "-1001", HealthcheckURL: server.URL + "/ping"}, "123:SECRET", &logs)
	n.TelegramAPI = server.URL
	results := n.Test(context.Background(), "vm-app-01", time.Date(2026, 10, 15, 19, 0, 0, 500, time.UTC))
	if len(results) != 2 || results[0].Target != "Webhook" || results[1].Target != "Telegram" || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("got %+v", results)
	}
	if len(rec.requests) != 2 {
		t.Fatalf("got %d requests, want 2 without the healthcheck", len(rec.requests))
	}
	want := `{"tool":"dbak","host":"vm-app-01","status":"test","started_at":"2026-10-15T19:00:00Z","finished_at":"2026-10-15T19:00:00Z","databases":[]}`
	if rec.requests[0].Body != want {
		t.Fatalf("got body %s", rec.requests[0].Body)
	}
	if !strings.Contains(rec.requests[1].Body, "This is a test message from dbak on vm-app-01.") {
		t.Fatalf("got Telegram body %s", rec.requests[1].Body)
	}
}

func TestNotifierTestWithoutTargets(t *testing.T) {
	var logs bytes.Buffer
	n := newNotifier(config.Notify{When: config.NotifyFailure}, "", &logs)
	if results := n.Test(context.Background(), "vm-app-01", time.Now()); len(results) != 0 {
		t.Fatalf("got %+v", results)
	}
}

func TestTelegramTopic(t *testing.T) {
	cases := []struct {
		name   string
		thread int
		want   string
	}{
		{"main chat", 0, `{"chat_id":"-1003749220292","text":"hello"}`},
		{"forum topic", 316, `{"chat_id":"-1003749220292","message_thread_id":316,"text":"hello"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, server := newServer(t)
			var logs bytes.Buffer
			n := newNotifier(config.Notify{TelegramChatID: "-1003749220292", TelegramThreadID: c.thread}, "123:SECRET", &logs)
			n.TelegramAPI = server.URL
			if err := n.SendTelegram(context.Background(), "hello"); err != nil {
				t.Fatal(err)
			}
			if rec.requests[0].Body != c.want {
				t.Fatalf("got %s, want %s", rec.requests[0].Body, c.want)
			}
		})
	}
}
