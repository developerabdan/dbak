package schedule

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/developerabdan/dbak/internal/app"
	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/system"
)

const (
	ModeSystemd = "systemd"
	ModeCron    = "cron"

	ServiceName = "dbak.service"
	TimerName   = "dbak.timer"

	minZoneSystemd = 235
)

type Paths struct {
	UnitDir       string
	CronFile      string
	LogrotateFile string
	LogDir        string
	Binary        string
	ConfigPath    string
	StateDir      string
}

func DefaultPaths() Paths {
	return Paths{
		UnitDir:       "/etc/systemd/system",
		CronFile:      "/etc/cron.d/dbak",
		LogrotateFile: "/etc/logrotate.d/dbak",
		LogDir:        "/var/log/dbak",
		Binary:        app.BinaryPath,
		ConfigPath:    app.ConfigPath,
		StateDir:      app.StateDir,
	}
}

func (p Paths) ServiceFile() string {
	return filepath.Join(p.UnitDir, ServiceName)
}

func (p Paths) TimerFile() string {
	return filepath.Join(p.UnitDir, TimerName)
}

func (p Paths) LogFile() string {
	return filepath.Join(p.LogDir, "dbak.log")
}

type Runner func(ctx context.Context, name string, args ...string) (string, error)

func RunCommand(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

type Installer struct {
	Paths      Paths
	Owner      system.Owner
	Systemd    bool
	SystemZone string
	Run        Runner
	LookPath   func(file string) (string, error)
	Now        func() time.Time
}

type Result struct {
	Mode       string
	Plan       Plan
	Expression string
	Next       time.Time
	Delay      time.Duration
	Warnings   []string
}

func (r Result) Summary() string {
	scheduler := "the systemd timer " + TimerName
	if r.Mode == ModeCron {
		scheduler = "the cron file"
	}
	text := fmt.Sprintf("dbak installed %s: %s.", scheduler, r.Plan.Describe())
	if !r.Next.IsZero() {
		text += " The next backup starts at " + FormatTime(r.Next.In(r.Plan.location()))
		if r.Delay > 0 {
			text += ", with a random delay of up to " + formatDelay(r.Delay)
		}
		text += "."
	}
	return text
}

func FormatTime(t time.Time) string {
	return t.Format("Mon 2006-01-02 15:04 MST")
}

func (i *Installer) now() time.Time {
	if i.Now == nil {
		return time.Now()
	}
	return i.Now()
}

func (i *Installer) run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := i.Run(ctx, name, args...)
	if err != nil {
		detail := lastLine(out)
		if detail == "" {
			detail = err.Error()
		}
		return out, fmt.Errorf("the command %s %s failed: %s", name, strings.Join(args, " "), detail)
	}
	return out, nil
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (i *Installer) Install(ctx context.Context, sc config.Schedule) (Result, error) {
	plan, err := NewPlan(sc)
	if err != nil {
		return Result{}, err
	}
	delay, err := config.ParseDelay(sc.RandomizedDelay)
	if err != nil {
		return Result{}, fmt.Errorf("the random delay %q is not valid", sc.RandomizedDelay)
	}
	var res Result
	if i.Systemd {
		res, err = i.installSystemd(ctx, plan, delay)
	} else {
		res, err = i.installCron(ctx, plan, delay)
	}
	res.Delay = delay
	return res, err
}

func (i *Installer) convert(plan Plan, reason string) (Plan, []string, error) {
	if i.SystemZone == "" || plan.Zone == i.SystemZone {
		return plan, nil, nil
	}
	converted, err := plan.In(i.SystemZone, i.now())
	if err != nil {
		return plan, nil, err
	}
	warnings := []string{fmt.Sprintf("%s dbak changed the schedule to the timezone of this server: %s, not %s.",
		reason, converted.Describe(), plan.Describe())}
	if UsesDaylightSaving(plan.Zone, i.now().Year()) || UsesDaylightSaving(i.SystemZone, i.now().Year()) {
		warnings = append(warnings, "Daylight saving time changes the offset between the two timezones. The backup time will move by one hour for a part of the year.")
	}
	return converted, warnings, nil
}

func (i *Installer) installSystemd(ctx context.Context, plan Plan, delay time.Duration) (Result, error) {
	version, err := i.systemdVersion(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Mode: ModeSystemd, Plan: plan}
	withZone := version >= minZoneSystemd
	if !withZone {
		reason := fmt.Sprintf("systemd %d cannot use a timezone in OnCalendar.", version)
		if res.Plan, res.Warnings, err = i.convert(plan, reason); err != nil {
			return Result{}, err
		}
	}
	res.Expression = res.Plan.OnCalendar(withZone && res.Plan.Zone != "")
	if _, err := i.run(ctx, "systemd-analyze", "calendar", res.Expression); err != nil {
		return Result{}, fmt.Errorf("systemd does not accept the schedule %q: %w. Change the schedule with dbak init", res.Expression, err)
	}
	if err := writeSystemFile(i.Paths.ServiceFile(), i.serviceUnit()); err != nil {
		return Result{}, err
	}
	if err := writeSystemFile(i.Paths.TimerFile(), timerUnit(res.Expression, delay)); err != nil {
		return Result{}, err
	}
	if err := removeIfPresent(i.Paths.CronFile); err != nil {
		return Result{}, err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", TimerName}, {"restart", TimerName}} {
		if _, err := i.run(ctx, "systemctl", args...); err != nil {
			return Result{}, fmt.Errorf("%w. Correct the problem, then run: sudo dbak schedule", err)
		}
	}
	res.Next = res.Plan.Next(i.now())
	return res, nil
}

func (i *Installer) systemdVersion(ctx context.Context) (int, error) {
	out, err := i.run(ctx, "systemctl", "--version")
	if err != nil {
		return 0, err
	}
	version, ok := ParseSystemdVersion(out)
	if !ok {
		return 0, errors.New("dbak cannot read the systemd version. Make sure that systemctl --version works")
	}
	return version, nil
}

func ParseSystemdVersion(out string) (int, bool) {
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "systemd" {
		return 0, false
	}
	version, err := strconv.Atoi(fields[1])
	return version, err == nil
}

func (i *Installer) execStart() string {
	args := []string{i.Paths.Binary}
	defaults := DefaultPaths()
	if i.Paths.ConfigPath != defaults.ConfigPath {
		args = append(args, "--config", i.Paths.ConfigPath)
	}
	if i.Paths.StateDir != defaults.StateDir {
		args = append(args, "--state-dir", i.Paths.StateDir)
	}
	return strings.Join(append(args, "run"), " ")
}

func (i *Installer) serviceUnit() string {
	return fmt.Sprintf(`[Unit]
Description=dbak database backup
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
User=%[1]s
Group=%[1]s
ExecStart=%[2]s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=%[3]s
`, system.ServiceUser, i.execStart(), i.Paths.StateDir)
}

func timerUnit(expression string, delay time.Duration) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=dbak backup schedule\n\n[Timer]\n")
	fmt.Fprintf(&b, "OnCalendar=%s\nPersistent=true\n", expression)
	if delay > 0 {
		fmt.Fprintf(&b, "RandomizedDelaySec=%s\n", formatDelay(delay))
	}
	b.WriteString("\n[Install]\nWantedBy=timers.target\n")
	return b.String()
}

func formatDelay(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
}

func (i *Installer) installCron(ctx context.Context, plan Plan, delay time.Duration) (Result, error) {
	if info, err := os.Stat(filepath.Dir(i.Paths.CronFile)); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("this system has no systemd and no %s directory. Install cron, then run: sudo dbak schedule", filepath.Dir(i.Paths.CronFile))
	}
	res := Result{Mode: ModeCron, Plan: plan}
	zoneLine := ""
	if i.cronHasTimezone(ctx) {
		zoneLine = "CRON_TZ=" + plan.Zone + "\n"
	} else {
		var err error
		if res.Plan, res.Warnings, err = i.convert(plan, "The cron daemon on this system does not support CRON_TZ."); err != nil {
			return Result{}, err
		}
	}
	command := i.execStart() + " >> " + i.Paths.LogFile() + " 2>&1"
	if delay > 0 {
		if _, err := i.LookPath("shuf"); err == nil {
			command = fmt.Sprintf("sleep $(shuf -i 0-%d -n 1) && %s", int(delay/time.Second), command)
		} else {
			res.Warnings = append(res.Warnings, "The shuf command is not installed. The cron schedule has no random delay.")
		}
	}
	res.Expression = res.Plan.CronFields()
	content := "SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n" +
		zoneLine + res.Expression + " " + system.ServiceUser + " " + command + "\n"
	if err := i.Owner.MkdirAll(i.Paths.LogDir, 0o750); err != nil {
		return Result{}, err
	}
	if err := i.prepareLogFile(); err != nil {
		return Result{}, err
	}
	if err := writeSystemFile(i.Paths.CronFile, content); err != nil {
		return Result{}, err
	}
	if info, err := os.Stat(filepath.Dir(i.Paths.LogrotateFile)); err == nil && info.IsDir() {
		if err := writeSystemFile(i.Paths.LogrotateFile, i.logrotateConfig()); err != nil {
			return Result{}, err
		}
	} else {
		res.Warnings = append(res.Warnings, "logrotate is not installed. Install logrotate, then run: sudo dbak schedule")
	}
	for _, unit := range []string{i.Paths.TimerFile(), i.Paths.ServiceFile()} {
		if err := removeIfPresent(unit); err != nil {
			return Result{}, err
		}
	}
	res.Next = res.Plan.Next(i.now())
	return res, nil
}

func (i *Installer) cronHasTimezone(ctx context.Context) bool {
	if _, err := i.LookPath("crond"); err != nil {
		return false
	}
	out, _ := i.Run(ctx, "crond", "-V")
	return strings.Contains(strings.ToLower(out), "cronie")
}

func (i *Installer) prepareLogFile() error {
	file, err := os.OpenFile(i.Paths.LogFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("cannot make the log file %s: %w", i.Paths.LogFile(), err)
	}
	file.Close()
	return i.Owner.Apply(i.Paths.LogFile(), 0o640)
}

func (i *Installer) logrotateConfig() string {
	return fmt.Sprintf(`%[1]s {
	weekly
	rotate 8
	compress
	missingok
	notifempty
	su %[2]s %[2]s
	create 0640 %[2]s %[2]s
}
`, i.Paths.LogFile(), system.ServiceUser)
}

func writeSystemFile(path, content string) error {
	return config.WriteFileAtomic(path, []byte(content), 0o644)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", path, err)
	}
	return nil
}

type Installed struct {
	Mode       string
	Expression string
	Enabled    string
	Active     string
	Next       string
}

func (i *Installer) Current(ctx context.Context) (Installed, error) {
	if _, err := os.Stat(i.Paths.TimerFile()); err == nil {
		return i.currentSystemd(ctx)
	}
	if _, err := os.Stat(i.Paths.CronFile); err == nil {
		expression, err := cronExpression(i.Paths.CronFile)
		if err != nil {
			return Installed{}, err
		}
		return Installed{Mode: ModeCron, Expression: expression}, nil
	}
	return Installed{}, nil
}

func (i *Installer) currentSystemd(ctx context.Context) (Installed, error) {
	expression, err := fileValue(i.Paths.TimerFile(), "OnCalendar=")
	if err != nil {
		return Installed{}, err
	}
	cur := Installed{Mode: ModeSystemd, Expression: expression}
	if !i.Systemd {
		cur.Enabled, cur.Active = "unknown", "unknown"
		return cur, nil
	}
	out, _ := i.Run(ctx, "systemctl", "is-enabled", TimerName)
	cur.Enabled = lastLine(out)
	out, _ = i.Run(ctx, "systemctl", "is-active", TimerName)
	cur.Active = lastLine(out)
	out, err = i.Run(ctx, "systemctl", "show", TimerName, "--property=NextElapseUSecRealtime", "--value")
	if err == nil {
		cur.Next = strings.TrimSpace(out)
	}
	return cur, nil
}

func fileValue(path, prefix string) (string, error) {
	var value string
	err := scanLines(path, func(line string) {
		if strings.HasPrefix(line, prefix) {
			value = strings.TrimPrefix(line, prefix)
		}
	})
	return value, err
}

func cronExpression(path string) (string, error) {
	var expression string
	err := scanLines(path, func(line string) {
		if line == "" || strings.Contains(strings.SplitN(line, " ", 2)[0], "=") {
			return
		}
		fields := strings.Fields(line)
		if len(fields) > 5 {
			expression = strings.Join(fields[:5], " ")
		}
	})
	return expression, err
}

func scanLines(path string, fn func(line string)) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fn(strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	return nil
}
