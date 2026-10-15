package status

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/schedule"
	"github.com/developerabdan/dbak/internal/state"
)

func testConfig() *config.Config {
	return &config.Config{
		Sources: []config.Source{
			{Name: config.LegacySourceName, Databases: []string{"app_prod", "app_audit", "app_new"}},
			{Name: "shop_mysql", Databases: []string{"app_prod"}},
		},
		Schedule: config.Schedule{Frequency: config.FrequencyDaily, Time: "02:00", Timezone: "Asia/Jakarta"},
	}
}

func testHistory() *state.File {
	return &state.File{Runs: []state.Run{
		{
			Host: "vm-app-01", Status: state.StatusSuccess,
			StartedAt: time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 13, 19, 1, 0, 0, time.UTC),
			Databases: []state.Database{
				{Name: "app_prod", Status: state.StatusSuccess, SizeBytes: 1024, Duration: time.Second},
				{Name: "app_audit", Status: state.StatusSuccess, SizeBytes: 2048, Duration: 2 * time.Second},
			},
		},
		{
			Host: "vm-app-01", Status: state.StatusFailure,
			StartedAt: time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 14, 19, 0, 5, 0, time.UTC),
			Databases: []state.Database{
				{Name: "app_prod", Status: state.StatusFailure, Error: "pg_dump: permission denied for table audit_log"},
				{Source: "shop_mysql", Name: "app_prod", Status: state.StatusSuccess, SizeBytes: 4096, Duration: time.Second},
			},
		},
	}}
}

var now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestBuild(t *testing.T) {
	r, err := Build(testConfig(), testHistory(), schedule.Installed{Mode: schedule.ModeSystemd, Active: "active"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.LastRun == nil || r.LastRun.Status != state.StatusFailure || r.Host != "vm-app-01" {
		t.Fatalf("got last run %+v", r.LastRun)
	}
	if len(r.Databases) != 4 {
		t.Fatalf("got %d databases, want 4", len(r.Databases))
	}
	prod, audit, fresh, shop := r.Databases[0], r.Databases[1], r.Databases[2], r.Databases[3]
	if shop.Source != "shop_mysql" || shop.Status != state.StatusSuccess || shop.SizeBytes != 4096 {
		t.Fatalf("got shop_mysql/app_prod %+v", shop)
	}
	if prod.Status != state.StatusFailure || prod.Error == "" || !prod.LastRunAt.Equal(time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC)) {
		t.Fatalf("got app_prod %+v", prod)
	}
	if audit.Status != state.StatusSuccess || audit.SizeBytes != 2048 || audit.DurationMS != 2000 {
		t.Fatalf("got app_audit %+v", audit)
	}
	if fresh.Status != never || fresh.LastRunAt != nil {
		t.Fatalf("got app_new %+v", fresh)
	}
	if r.Schedule.NextRun == nil || !r.Schedule.NextRun.Equal(time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC)) {
		t.Fatalf("got next run %v", r.Schedule.NextRun)
	}
}

func TestWriteText(t *testing.T) {
	r, err := Build(testConfig(), testHistory(), schedule.Installed{Mode: schedule.ModeSystemd, Active: "active"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.WriteText(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SOURCE      DATABASE   STATUS   LAST RUN              SIZE     DURATION",
		"default     app_prod   failure  2026-10-15 02:00 WIB  -        -",
		"default     app_audit  success  2026-10-14 02:00 WIB  2.0 KiB  2s",
		"default     app_new    never    never                 -        -",
		"shop_mysql  app_prod   success  2026-10-15 02:00 WIB  4.0 KiB  1s",
		"default/app_prod: pg_dump: permission denied for table audit_log.",
		"Last run:     2026-10-15 02:00 WIB, failure",
		"Schedule:     daily at 02:00 Asia/Jakarta",
		"Next backup:  2026-10-16 02:00 WIB (systemd)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestWriteTextWithoutSchedule(t *testing.T) {
	cases := map[string]schedule.Installed{
		"No schedule is installed":        {},
		"The systemd timer is not active": {Mode: schedule.ModeSystemd, Active: "inactive"},
	}
	for want, installed := range cases {
		r, err := Build(testConfig(), &state.File{}, installed, now)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := r.WriteText(&out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "Last run:     never.") {
			t.Fatalf("got output:\n%s", out.String())
		}
	}
}

func TestJSON(t *testing.T) {
	r, err := Build(testConfig(), testHistory(), schedule.Installed{Mode: schedule.ModeCron}, now)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"last_run":{"status":"failure","started_at":"2026-10-14T19:00:00Z"`,
		`{"source":"default","name":"app_new","status":"never","last_run_at":null}`,
		`{"source":"shop_mysql","name":"app_prod","status":"success"`,
		`"next_run":"2026-10-16T02:00:00+07:00"`,
		`"scheduler":"cron","active":true`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the JSON does not contain %s:\n%s", want, data)
		}
	}
}
