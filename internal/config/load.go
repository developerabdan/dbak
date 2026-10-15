package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	cfg, err := Read(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Read(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("the config file %s does not exist. Run \"dbak init\" to make it", path)
		}
		return nil, fmt.Errorf("cannot read the config file %s: %w. Make sure that the file has read permission", path, err)
	}
	return Decode(data)
}

func Parse(data []byte) (*Config, error) {
	cfg, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

type document struct {
	Config `yaml:",inline"`
	Source *Source `yaml:"source"`
}

func Decode(data []byte) (*Config, error) {
	var doc document
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the config file is empty. Run \"dbak init\" to make it")
		}
		return nil, fmt.Errorf("the config file has a YAML error: %w. Correct the line in the message", err)
	}
	cfg := doc.Config
	if err := convertLegacy(&cfg, doc.Source); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func convertLegacy(cfg *Config, legacy *Source) error {
	if cfg.Version != LegacyVersion {
		if legacy != nil {
			return fmt.Errorf("the config file has a source block, but version %d uses a sources list. Move the source into sources", cfg.Version)
		}
		return nil
	}
	if len(cfg.Sources) > 0 {
		return errors.New("the config file has version 1 and a sources list. Set version: 2")
	}
	if legacy != nil {
		converted := *legacy
		if converted.Name == "" {
			converted.Name = LegacySourceName
		}
		cfg.Sources = []Source{converted}
	}
	cfg.Version = CurrentVersion
	return nil
}

func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot encode the config: %w", err)
	}
	return WriteFileAtomic(path, data, 0o600)
}

func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("cannot write %s: %w. Make sure that the directory exists and has write permission", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot set the mode of %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	return nil
}

func ReadSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("the secret file %s does not exist. Make the file or run \"dbak init\" again", path)
		}
		return "", fmt.Errorf("cannot read the secret file %s: %w. Make sure that the dbak user can read the file", path, err)
	}
	secret := strings.TrimRight(string(data), "\r\n")
	if secret == "" {
		return "", fmt.Errorf("the secret file %s is empty. Write the secret into the file", path)
	}
	return secret, nil
}
