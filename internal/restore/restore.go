package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"

	"github.com/developerabdan/dbak/internal/encrypt"
	"github.com/developerabdan/dbak/internal/system"
)

const (
	zstdSuffix    = ".zst"
	partialSuffix = ".partial"
	fileMode      = 0o600
	dirMode       = 0o700
)

type Store interface {
	Open(path string) (io.ReadCloser, error)
}

type Request struct {
	RemoteFile string
	Dir        string
	Identity   age.Identity
	Owner      system.Owner
}

type Result struct {
	Path      string
	SizeBytes int64
}

func Encrypted(name string) bool {
	return strings.HasSuffix(name, "."+encrypt.Extension)
}

func OutputName(name string) string {
	name = strings.TrimSuffix(name, "."+encrypt.Extension)
	return strings.TrimSuffix(name, zstdSuffix)
}

func OutputPath(dir, remoteFile string) string {
	return filepath.Join(dir, OutputName(path.Base(remoteFile)))
}

func CheckTarget(dir, remoteFile string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("the directory must be an absolute path that starts with /")
	}
	target := OutputPath(dir, remoteFile)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("the file %s exists. Remove the file or type a different directory", target)
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return fmt.Errorf("%s is not a directory. Type a different directory", dir)
	}
	return nil
}

func Download(ctx context.Context, store Store, req Request) (Result, error) {
	name := path.Base(req.RemoteFile)
	if Encrypted(name) && req.Identity == nil {
		return Result{}, errors.New("the backup is encrypted. Type the age private key")
	}
	if err := CheckTarget(req.Dir, req.RemoteFile); err != nil {
		return Result{}, err
	}
	if err := makeDir(req.Dir, req.Owner); err != nil {
		return Result{}, err
	}
	src, err := store.Open(req.RemoteFile)
	if err != nil {
		return Result{}, fmt.Errorf("cannot read %s on the SFTP server: %w", req.RemoteFile, err)
	}
	defer src.Close()
	data, closeData, err := decode(contextReader{ctx: ctx, r: src}, name, req.Identity)
	if err != nil {
		return Result{}, err
	}
	defer closeData()
	target := OutputPath(req.Dir, req.RemoteFile)
	size, err := writeFile(target, data, req.Owner)
	if err != nil {
		return Result{}, err
	}
	return Result{Path: target, SizeBytes: size}, nil
}

func makeDir(dir string, owner system.Owner) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	return owner.MkdirAll(dir, dirMode)
}

func decode(r io.Reader, name string, identity age.Identity) (io.Reader, func(), error) {
	if Encrypted(name) {
		decrypted, err := encrypt.NewReader(r, identity)
		if err != nil {
			return nil, nil, err
		}
		r = decrypted
		name = strings.TrimSuffix(name, "."+encrypt.Extension)
	}
	if !strings.HasSuffix(name, zstdSuffix) {
		return r, func() {}, nil
	}
	decoder, err := zstd.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot start the decompression: %w", err)
	}
	return decoder, decoder.Close, nil
}

func writeFile(target string, data io.Reader, owner system.Owner) (int64, error) {
	partial := target + partialSuffix
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return 0, fmt.Errorf("cannot write %s: %w", partial, err)
	}
	size, err := copyAndClose(f, data)
	if err == nil {
		err = owner.Apply(partial, fileMode)
	}
	if err == nil {
		err = os.Rename(partial, target)
	}
	if err != nil {
		os.Remove(partial)
		return 0, err
	}
	return size, nil
}

func copyAndClose(f *os.File, data io.Reader) (int64, error) {
	size, err := io.Copy(f, data)
	if err != nil {
		f.Close()
		return 0, readError(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, fmt.Errorf("cannot write %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("cannot write %s: %w", f.Name(), err)
	}
	return size, nil
}

func readError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("the restore stopped before the end. dbak removed the incomplete file")
	}
	return fmt.Errorf("the download stopped before the end: %w. dbak removed the incomplete file", err)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
