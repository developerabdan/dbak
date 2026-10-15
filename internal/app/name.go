package app

const Name = "dbak"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

const (
	BinaryPath = "/usr/local/bin/dbak"
	ConfigDir  = "/etc/dbak"
	ConfigPath = "/etc/dbak/config.yaml"
	SecretsDir = "/etc/dbak/secrets"
	StateDir   = "/var/lib/dbak"
	StateFile  = "state.json"
	LockFile   = "dbak.lock"
)
