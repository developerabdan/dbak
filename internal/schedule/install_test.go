package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/system"
)

type fakeRunner struct {
	calls     []string
	outputs   map[string]string
	failures  map[string]bool
	installed map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, failures: map[string]bool{}, installed: map[string]bool{}}
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	out := f.outputs[line]
	if f.failures[line] {
		return out, errors.New("exit status 1")
	}
	return out, nil
}

func (f *fakeRunner) lookPath(file string) (string, error) {
	if f.installed[file] {
		return "/usr/bin/" + file, nil
	}
	return "", errors.New("not found")
}

func newInstaller(t *testing.T, systemd bool, zone string) (*Installer, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"systemd", "cron.d", "logrotate.d"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	paths := DefaultPaths()
	paths.UnitDir = filepath.Join(root, "systemd")
	paths.CronFile = filepath.Join(root, "cron.d", "dbak")
	paths.LogrotateFile = filepath.Join(root, "logrotate.d", "dbak")
	paths.LogDir = filepath.Join(root, "log")
	runner := newFakeRunner()
	runner.outputs["systemctl --version"] = "systemd 255 (255.4-1ubuntu8)\n+PAM +AUDIT\n"
	return &Installer{
		Paths:      paths,
		Owner:      system.CurrentOwner(),
		Systemd:    systemd,
		SystemZone: zone,
		Run:        runner.run,
		LookPath:   runner.lookPath,
		Now:        func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) },
	}, runner
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func withDelay(sc config.Schedule, delay string) config.Schedule {
	sc.RandomizedDelay = delay
	return sc
}

func TestInstallSystemd(t *testing.T) {
	inst, runner := newInstaller(t, true, "UTC")
	if err := os.WriteFile(inst.Paths.CronFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := inst.Install(context.Background(), withDelay(daily("02:00", "Asia/Jakarta"), "5m"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeSystemd || res.Expression != "*-*-* 02:00:00 Asia/Jakarta" || len(res.Warnings) != 0 {
		t.Fatalf("got %+v", res)
	}
	if want := time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC); !res.Next.Equal(want) {
		t.Fatalf("got next %s, want %s", res.Next, want)
	}
	timer := readFile(t, inst.Paths.TimerFile())
	for _, want := range []string{"OnCalendar=*-*-* 02:00:00 Asia/Jakarta\n", "Persistent=true\n", "RandomizedDelaySec=5m\n", "WantedBy=timers.target\n"} {
		if !strings.Contains(timer, want) {
			t.Fatalf("the timer has no %q:\n%s", want, timer)
		}
	}
	service := readFile(t, inst.Paths.ServiceFile())
	for _, want := range []string{"User=dbak\n", "ExecStart=/usr/local/bin/dbak run\n", "ReadWritePaths=/var/lib/dbak\n", "ProtectSystem=strict\n"} {
		if !strings.Contains(service, want) {
			t.Fatalf("the service has no %q:\n%s", want, service)
		}
	}
	if _, err := os.Stat(inst.Paths.CronFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old cron file is still present")
	}
	wantCalls := []string{
		"systemctl --version",
		"systemd-analyze calendar *-*-* 02:00:00 Asia/Jakarta",
		"systemctl daemon-reload",
		"systemctl enable --now dbak.timer",
		"systemctl restart dbak.timer",
	}
	if !slices.Equal(runner.calls, wantCalls) {
		t.Fatalf("got calls %q", runner.calls)
	}
}

func TestInstallSystemdWithoutDelay(t *testing.T) {
	inst, _ := newInstaller(t, true, "UTC")
	if _, err := inst.Install(context.Background(), withDelay(everyHours(6, "UTC"), "0")); err != nil {
		t.Fatal(err)
	}
	timer := readFile(t, inst.Paths.TimerFile())
	if strings.Contains(timer, "RandomizedDelaySec") || !strings.Contains(timer, "OnCalendar=*-*-* 00/6:00:00 UTC\n") {
		t.Fatalf("got timer:\n%s", timer)
	}
}

func TestInstallSystemdWithCustomPaths(t *testing.T) {
	inst, _ := newInstaller(t, true, "UTC")
	inst.Paths.ConfigPath = "/opt/dbak/config.yaml"
	inst.Paths.StateDir = "/opt/dbak/state"
	if _, err := inst.Install(context.Background(), daily("02:00", "UTC")); err != nil {
		t.Fatal(err)
	}
	service := readFile(t, inst.Paths.ServiceFile())
	if !strings.Contains(service, "ExecStart=/usr/local/bin/dbak --config /opt/dbak/config.yaml --state-dir /opt/dbak/state run\n") ||
		!strings.Contains(service, "ReadWritePaths=/opt/dbak/state\n") {
		t.Fatalf("got service:\n%s", service)
	}
}

func TestInstallOldSystemdConvertsTheTime(t *testing.T) {
	inst, runner := newInstaller(t, true, "UTC")
	runner.outputs["systemctl --version"] = "systemd 219\n"
	res, err := inst.Install(context.Background(), weekly("Sun", "02:00", "Asia/Jakarta"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Expression != "Sat *-*-* 19:00:00" {
		t.Fatalf("got expression %q", res.Expression)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "systemd 219") || !strings.Contains(res.Warnings[0], "weekly on Sat at 19:00 UTC") {
		t.Fatalf("got warnings %q", res.Warnings)
	}
}

func TestInstallOldSystemdWithDaylightSaving(t *testing.T) {
	inst, runner := newInstaller(t, true, "UTC")
	runner.outputs["systemctl --version"] = "systemd 219\n"
	res, err := inst.Install(context.Background(), daily("02:00", "Europe/Berlin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[1], "Daylight saving time") {
		t.Fatalf("got warnings %q", res.Warnings)
	}
}

func TestInstallSystemdRejectedExpression(t *testing.T) {
	inst, runner := newInstaller(t, true, "UTC")
	runner.failures["systemd-analyze calendar *-*-* 02:00:00 UTC"] = true
	runner.outputs["systemd-analyze calendar *-*-* 02:00:00 UTC"] = "Failed to parse calendar specification\n"
	_, err := inst.Install(context.Background(), daily("02:00", "UTC"))
	if err == nil || !strings.Contains(err.Error(), "Failed to parse calendar specification") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(inst.Paths.TimerFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dbak wrote the timer after systemd rejected the schedule")
	}
}

func TestInstallSystemdEnableFails(t *testing.T) {
	inst, runner := newInstaller(t, true, "UTC")
	runner.failures["systemctl enable --now dbak.timer"] = true
	_, err := inst.Install(context.Background(), daily("02:00", "UTC"))
	if err == nil || !strings.Contains(err.Error(), "sudo dbak schedule") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallCronWithTimezone(t *testing.T) {
	inst, runner := newInstaller(t, false, "UTC")
	runner.installed["crond"] = true
	runner.outputs["crond -V"] = "cronie 1.5.7\n"
	if err := os.WriteFile(inst.Paths.TimerFile(), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := inst.Install(context.Background(), daily("02:00", "Asia/Jakarta"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeCron || len(res.Warnings) != 0 {
		t.Fatalf("got %+v", res)
	}
	cron := readFile(t, inst.Paths.CronFile)
	want := "CRON_TZ=Asia/Jakarta\n0 2 * * * dbak /usr/local/bin/dbak run >> " + inst.Paths.LogFile() + " 2>&1\n"
	if !strings.HasSuffix(cron, want) {
		t.Fatalf("got cron file:\n%s", cron)
	}
	logrotate := readFile(t, inst.Paths.LogrotateFile)
	for _, want := range []string{inst.Paths.LogFile() + " {", "weekly", "rotate 8", "compress", "create 0640 dbak dbak"} {
		if !strings.Contains(logrotate, want) {
			t.Fatalf("the logrotate config has no %q:\n%s", want, logrotate)
		}
	}
	info, err := os.Stat(inst.Paths.LogFile())
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("got log file %v %v", info, err)
	}
	if _, err := os.Stat(inst.Paths.TimerFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the old timer is still present")
	}
}

func TestInstallCronWithoutTimezoneSupport(t *testing.T) {
	inst, runner := newInstaller(t, false, "UTC")
	runner.installed["shuf"] = true
	res, err := inst.Install(context.Background(), withDelay(daily("02:00", "Asia/Jakarta"), "15m"))
	if err != nil {
		t.Fatal(err)
	}
	cron := readFile(t, inst.Paths.CronFile)
	if strings.Contains(cron, "CRON_TZ") || !strings.Contains(cron, "\n0 19 * * * dbak sleep $(shuf -i 0-900 -n 1) && /usr/local/bin/dbak run >> ") {
		t.Fatalf("got cron file:\n%s", cron)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "CRON_TZ") {
		t.Fatalf("got warnings %q", res.Warnings)
	}
}

func TestInstallCronSameTimezone(t *testing.T) {
	inst, _ := newInstaller(t, false, "Asia/Jakarta")
	res, err := inst.Install(context.Background(), withDelay(daily("02:00", "Asia/Jakarta"), "5m"))
	if err != nil {
		t.Fatal(err)
	}
	cron := readFile(t, inst.Paths.CronFile)
	if !strings.Contains(cron, "\n0 2 * * * dbak /usr/local/bin/dbak run") {
		t.Fatalf("got cron file:\n%s", cron)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "shuf") {
		t.Fatalf("got warnings %q", res.Warnings)
	}
}

func TestInstallCronWithoutCron(t *testing.T) {
	inst, _ := newInstaller(t, false, "UTC")
	inst.Paths.CronFile = filepath.Join(t.TempDir(), "missing", "dbak")
	_, err := inst.Install(context.Background(), daily("02:00", "UTC"))
	if err == nil || !strings.Contains(err.Error(), "Install cron") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallCronWithoutLogrotate(t *testing.T) {
	inst, _ := newInstaller(t, false, "UTC")
	inst.Paths.LogrotateFile = filepath.Join(t.TempDir(), "missing", "dbak")
	res, err := inst.Install(context.Background(), daily("02:00", "UTC"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "logrotate") {
		t.Fatalf("got warnings %q", res.Warnings)
	}
}

func TestCurrent(t *testing.T) {
	t.Run("nothing installed", func(t *testing.T) {
		inst, _ := newInstaller(t, true, "UTC")
		cur, err := inst.Current(context.Background())
		if err != nil || cur.Mode != "" {
			t.Fatalf("got %+v %v", cur, err)
		}
	})
	t.Run("systemd timer", func(t *testing.T) {
		inst, runner := newInstaller(t, true, "UTC")
		if _, err := inst.Install(context.Background(), daily("02:00", "Asia/Jakarta")); err != nil {
			t.Fatal(err)
		}
		runner.outputs["systemctl is-enabled dbak.timer"] = "enabled\n"
		runner.outputs["systemctl is-active dbak.timer"] = "active\n"
		runner.outputs["systemctl show dbak.timer --property=NextElapseUSecRealtime --value"] = "Fri 2026-10-16 02:00:00 WIB\n"
		cur, err := inst.Current(context.Background())
		want := Installed{Mode: ModeSystemd, Expression: "*-*-* 02:00:00 Asia/Jakarta", Enabled: "enabled", Active: "active", Next: "Fri 2026-10-16 02:00:00 WIB"}
		if err != nil || cur != want {
			t.Fatalf("got %+v %v", cur, err)
		}
	})
	t.Run("cron file", func(t *testing.T) {
		inst, _ := newInstaller(t, false, "UTC")
		if _, err := inst.Install(context.Background(), weekly("Sun", "02:00", "UTC")); err != nil {
			t.Fatal(err)
		}
		cur, err := inst.Current(context.Background())
		if err != nil || cur.Mode != ModeCron || cur.Expression != "0 2 * * 0" {
			t.Fatalf("got %+v %v", cur, err)
		}
	})
}

func TestParseSystemdVersion(t *testing.T) {
	cases := map[string]int{
		"systemd 255 (255.4-1ubuntu8)\n+PAM": 255,
		"systemd 219\n+PAM +AUDIT":           219,
	}
	for in, want := range cases {
		if got, ok := ParseSystemdVersion(in); !ok || got != want {
			t.Fatalf("got %d %v for %q", got, ok, in)
		}
	}
	if _, ok := ParseSystemdVersion("bash: systemctl: not found"); ok {
		t.Fatal("got ok for bad output")
	}
}

func TestResultSummary(t *testing.T) {
	inst, _ := newInstaller(t, true, "UTC")
	res, err := inst.Install(context.Background(), withDelay(daily("02:00", "Asia/Jakarta"), "5m"))
	if err != nil {
		t.Fatal(err)
	}
	want := "dbak installed the systemd timer dbak.timer: daily at 02:00 Asia/Jakarta. The next backup starts at Fri 2026-10-16 02:00 WIB, with a random delay of up to 5m."
	if got := res.Summary(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRemove(t *testing.T) {
	t.Run("systemd", func(t *testing.T) {
		inst, runner := newInstaller(t, true, "UTC")
		if _, err := inst.Install(context.Background(), daily("02:00", "UTC")); err != nil {
			t.Fatal(err)
		}
		runner.calls = nil
		removed, err := inst.Remove(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(removed, []string{inst.Paths.TimerFile(), inst.Paths.ServiceFile()}) {
			t.Fatalf("got removed %v", removed)
		}
		if !slices.Equal(runner.calls, []string{"systemctl disable --now dbak.timer", "systemctl daemon-reload"}) {
			t.Fatalf("got calls %q", runner.calls)
		}
	})
	t.Run("cron", func(t *testing.T) {
		inst, runner := newInstaller(t, false, "UTC")
		if _, err := inst.Install(context.Background(), daily("02:00", "UTC")); err != nil {
			t.Fatal(err)
		}
		runner.calls = nil
		removed, err := inst.Remove(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(removed, []string{inst.Paths.CronFile, inst.Paths.LogrotateFile}) || len(runner.calls) != 0 {
			t.Fatalf("got removed %v and calls %q", removed, runner.calls)
		}
	})
	t.Run("nothing installed", func(t *testing.T) {
		inst, runner := newInstaller(t, true, "UTC")
		removed, err := inst.Remove(context.Background())
		if err != nil || len(removed) != 0 || len(runner.calls) != 0 {
			t.Fatalf("got %v %v %q", removed, err, runner.calls)
		}
	})
}
