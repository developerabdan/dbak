package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/state"
)

const (
	Timeout           = 10 * time.Second
	Retries           = 2
	DefaultTelegram   = "https://api.telegram.org"
	maxErrorLength    = 200
	defaultRetryDelay = 2 * time.Second
)

type Notifier struct {
	Config        config.Notify
	TelegramToken string
	Client        *http.Client
	TelegramAPI   string
	RetryDelay    time.Duration
	Logger        *slog.Logger
}

func New(cfg config.Notify, telegramToken string, logger *slog.Logger) *Notifier {
	return &Notifier{
		Config:        cfg,
		TelegramToken: telegramToken,
		Client:        &http.Client{Timeout: Timeout},
		TelegramAPI:   DefaultTelegram,
		RetryDelay:    defaultRetryDelay,
		Logger:        logger,
	}
}

type Payload struct {
	Tool       string     `json:"tool"`
	Host       string     `json:"host"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
	Databases  []Database `json:"databases"`
}

type Database struct {
	Source     string `json:"source,omitempty"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

func NewPayload(run state.Run) Payload {
	p := Payload{
		Tool:       app.Name,
		Host:       run.Host,
		Status:     run.Status,
		StartedAt:  run.StartedAt.UTC().Truncate(time.Second),
		FinishedAt: run.FinishedAt.UTC().Truncate(time.Second),
		Databases:  make([]Database, 0, len(run.Databases)),
	}
	for _, db := range run.Databases {
		p.Databases = append(p.Databases, Database{
			Source:     db.Source,
			Name:       db.Name,
			Status:     db.Status,
			SizeBytes:  db.SizeBytes,
			RemotePath: db.RemotePath,
			Error:      db.Error,
		})
	}
	return p
}

func (n *Notifier) Start(ctx context.Context) {
	if n.Config.HealthcheckURL == "" {
		return
	}
	n.report("healthcheck", n.post(ctx, healthcheckURL(n.Config.HealthcheckURL, "start"), "text/plain", nil))
}

func (n *Notifier) Finish(ctx context.Context, run state.Run) {
	if n.Config.HealthcheckURL != "" {
		suffix := ""
		if run.Status != state.StatusSuccess {
			suffix = "fail"
		}
		n.report("healthcheck", n.post(ctx, healthcheckURL(n.Config.HealthcheckURL, suffix), "text/plain", []byte(TelegramText(run))))
	}
	if !n.wanted(run.Status) {
		return
	}
	if n.Config.WebhookURL != "" {
		n.report("webhook", n.sendWebhook(ctx, run))
	}
	if n.Config.TelegramChatID != "" {
		n.report("Telegram", n.SendTelegram(ctx, TelegramText(run)))
	}
}

func (n *Notifier) wanted(status string) bool {
	switch n.Config.When {
	case config.NotifyAlways:
		return true
	case config.NotifyNever:
		return false
	}
	return status != state.StatusSuccess
}

func (n *Notifier) report(target string, err error) {
	if err != nil {
		n.Logger.Warn(fmt.Sprintf("The %s notification failed. The backup result does not change.", target), "error", err.Error())
		return
	}
	n.Logger.Info(fmt.Sprintf("The %s notification was sent.", target))
}

func (n *Notifier) sendWebhook(ctx context.Context, run state.Run) error {
	body, err := json.Marshal(NewPayload(run))
	if err != nil {
		return err
	}
	return n.post(ctx, n.Config.WebhookURL, "application/json", body)
}

func (n *Notifier) SendTelegram(ctx context.Context, text string) error {
	if n.TelegramToken == "" {
		return errors.New("the Telegram bot token is empty. Run dbak init and type the token")
	}
	body, err := json.Marshal(map[string]string{"chat_id": n.Config.TelegramChatID, "text": text})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(n.TelegramAPI, "/") + "/bot" + n.TelegramToken + "/sendMessage"
	return n.post(ctx, endpoint, "application/json", body)
}

func healthcheckURL(base, suffix string) string {
	if suffix == "" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + suffix
}

func (n *Notifier) post(ctx context.Context, endpoint, contentType string, body []byte) error {
	var err error
	for attempt := 0; attempt <= Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(n.RetryDelay):
			}
		}
		var retry bool
		retry, err = n.postOnce(ctx, endpoint, contentType, body)
		if err == nil || !retry {
			return err
		}
	}
	return err
}

func (n *Notifier) postOnce(ctx context.Context, endpoint, contentType string, body []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, errors.New("the URL is not valid. Correct the URL with dbak init")
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", app.Name+"/"+app.Version)
	resp, err := n.Client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return true, fmt.Errorf("the request did not complete: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
	if description := errorDescription(data); description != "" {
		return retry, fmt.Errorf("the server returned HTTP status %d: %s", resp.StatusCode, description)
	}
	return retry, fmt.Errorf("the server returned HTTP status %d", resp.StatusCode)
}

func errorDescription(data []byte) string {
	var body struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(data, &body) != nil {
		return ""
	}
	return truncate(strings.TrimSpace(body.Description), maxErrorLength)
}

func TelegramText(run state.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dbak on %s: %s\n", run.Host, statusText(run.Status))
	fmt.Fprintf(&b, "Started: %s\n", run.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	for _, db := range run.Databases {
		name := DatabaseLabel(db)
		if db.Status == state.StatusSuccess {
			fmt.Fprintf(&b, "\n%s: success, %s", name, app.HumanSize(db.SizeBytes))
			continue
		}
		fmt.Fprintf(&b, "\n%s: failure. %s", name, truncate(db.Error, maxErrorLength))
	}
	return b.String()
}

func DatabaseLabel(db state.Database) string {
	if db.Source == "" {
		return db.Name
	}
	return db.Source + "/" + db.Name
}

func statusText(status string) string {
	switch status {
	case state.StatusSuccess:
		return "The backup completed."
	case state.StatusPartial:
		return "The backup of some databases failed."
	}
	return "The backup failed."
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

const StatusTest = "test"

type TestResult struct {
	Target string
	Err    error
}

func (n *Notifier) Test(ctx context.Context, host string, now time.Time) []TestResult {
	var results []TestResult
	if n.Config.WebhookURL != "" {
		at := now.UTC().Truncate(time.Second)
		body, err := json.Marshal(Payload{Tool: app.Name, Host: host, Status: StatusTest, StartedAt: at, FinishedAt: at, Databases: []Database{}})
		if err == nil {
			err = n.post(ctx, n.Config.WebhookURL, "application/json", body)
		}
		results = append(results, TestResult{Target: "Webhook", Err: err})
	}
	if n.Config.TelegramChatID != "" {
		results = append(results, TestResult{Target: "Telegram", Err: n.SendTelegram(ctx, TelegramTestText(host))})
	}
	return results
}

func TelegramTestText(host string) string {
	return fmt.Sprintf("This is a test message from dbak on %s. The Telegram notification works.", host)
}
