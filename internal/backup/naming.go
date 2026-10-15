package backup

import (
	"fmt"
	"path"
	"time"
)

const (
	timestampLayout = "20060102T150405Z"
	PartialSuffix   = ".partial"
)

func FileName(database string, at time.Time, extension string) string {
	return fmt.Sprintf("%s_%s.%s", database, at.UTC().Format(timestampLayout), extension)
}

func RemoteDir(remotePath, hostname, source, database string) string {
	return path.Join(remotePath, hostname, source, database)
}

func RemoteFile(remotePath, hostname, source, database string, at time.Time, extension string) string {
	return path.Join(RemoteDir(remotePath, hostname, source, database), FileName(database, at, extension))
}
