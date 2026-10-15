package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"

	"dbak/internal/app"
	"dbak/internal/config"
	"dbak/internal/sftp"
)

const (
	keyCurrent  = "current"
	keyGenerate = "generate"
	keyExisting = "existing"
)

func (s *session) configureDestination(ctx context.Context) error {
	ui := s.env.UI
	ui.Heading("SFTP destination")
	for {
		if err := s.askDestination(); err != nil {
			return err
		}
		if err := s.configureKey(); err != nil {
			return err
		}
		err := s.pinHostKey()
		if err == nil {
			err = s.testUpload()
		}
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrAborted) {
			return err
		}
		ui.Error(app.Sentence(err.Error()))
		choice, err := ui.Select("The SFTP check failed. What do you want to do?", "",
			options("Change the SFTP settings", "change", "Stop dbak init", "stop"), "change")
		if err != nil {
			return err
		}
		if choice == "stop" {
			return ErrAborted
		}
	}
}

func (s *session) askDestination() error {
	ui := s.env.UI
	d := &s.cfg.Destination
	var err error
	if d.Host, err = ui.Input("SFTP host", "Type the IP address or the host name of the SFTP server.", d.Host, required); err != nil {
		return err
	}
	port, err := ui.Input("SFTP port", "", strconv.Itoa(d.Port), validPort)
	if err != nil {
		return err
	}
	d.Port, _ = strconv.Atoi(port)
	if d.Username, err = ui.Input("SFTP user name", "", d.Username, required); err != nil {
		return err
	}
	d.RemotePath, err = ui.Input("Remote path", "dbak puts the backups in <remote path>/"+s.env.Hostname+"/.", d.RemotePath, absolutePath)
	return err
}

func (s *session) keyFile() string {
	return s.env.Paths.Secret(privateKeyFileName)
}

func (s *session) keyComment() string {
	return "dbak@" + s.env.Hostname
}

func (s *session) configureKey() error {
	ui := s.env.UI
	opts := options("Generate a new key", keyGenerate, "Use an existing key file", keyExisting)
	choice := keyGenerate
	if _, err := os.Stat(s.keyFile()); err == nil {
		opts = append([]Option{{Label: "Use the current dbak key", Value: keyCurrent}}, opts...)
		choice = keyCurrent
	}
	choice, err := ui.Select("SFTP authentication", "dbak uses SSH key authentication. Password authentication is not supported.", opts, choice)
	if err != nil {
		return err
	}
	switch choice {
	case keyGenerate:
		err = s.generateKey()
	case keyExisting:
		err = s.importKey()
	default:
		err = s.showCurrentKey()
	}
	if err != nil {
		return err
	}
	s.cfg.Destination.PrivateKeyFile = s.keyFile()
	s.cfg.Destination.KnownHostsFile = s.env.Paths.Secret(knownHostsFileName)
	return nil
}

func (s *session) generateKey() error {
	ui := s.env.UI
	if _, err := os.Stat(s.keyFile()); err == nil {
		replace, err := ui.Confirm("Replace the current dbak key?", "The SFTP server will reject the old key after this change.", false)
		if err != nil {
			return err
		}
		if !replace {
			return s.showCurrentKey()
		}
	}
	privatePEM, authorized, err := sftp.GenerateKey(s.keyComment())
	if err != nil {
		return err
	}
	if err := s.ensureSecretsDir(); err != nil {
		return err
	}
	if err := s.writeSecret(s.keyFile(), privatePEM); err != nil {
		return err
	}
	if err := s.writeFile(s.keyFile()+".pub", []byte(authorized+"\n"), 0o644); err != nil {
		return err
	}
	ui.Success("dbak made a new ed25519 key in " + s.keyFile() + ".")
	s.showAuthorize(authorized)
	return ui.WaitEnter("Add the public key to the SFTP server. Then push Enter.")
}

func (s *session) showCurrentKey() error {
	authorized, err := sftp.PublicKeyFromFile(s.keyFile(), s.keyComment())
	if err != nil {
		return err
	}
	s.env.UI.Info("dbak uses the key in " + s.keyFile() + ".")
	s.showAuthorize(authorized)
	return nil
}

func (s *session) showAuthorize(authorized string) {
	ui := s.env.UI
	ui.Info("This is the public key:")
	ui.Code(authorized)
	ui.Info(fmt.Sprintf("To add the key, log in to the SFTP server as %s and run this command:", s.cfg.Destination.Username))
	ui.Code(sftp.AuthorizeCommand(authorized))
}

func (s *session) importKey() error {
	ui := s.env.UI
	source, err := ui.Input("Path of the private key file", "The key must not have a passphrase.", "", func(v string) error {
		if !path.IsAbs(v) {
			return errors.New("type an absolute path")
		}
		_, err := sftp.PublicKeyFromFile(v, "")
		return err
	})
	if err != nil {
		return err
	}
	if source == s.keyFile() {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", source, err)
	}
	if err := s.ensureSecretsDir(); err != nil {
		return err
	}
	if err := s.writeSecret(s.keyFile(), data); err != nil {
		return err
	}
	ui.Success("dbak copied the key to " + s.keyFile() + ", so that the dbak user can read it.")
	return nil
}

func (s *session) pinHostKey() error {
	ui := s.env.UI
	d := s.cfg.Destination
	key, err := s.env.FetchHostKey(d.Host, d.Port)
	if err != nil {
		return err
	}
	ui.Info(fmt.Sprintf("The SFTP server %s:%d has this host key:", d.Host, d.Port))
	ui.Code(key.Type() + " " + sftp.Fingerprint(key))
	ok, err := ui.Confirm("Is this the correct host key?",
		"On the SFTP server, run ssh-keygen -lf on the host key file and compare the fingerprint.", false)
	if err != nil {
		return err
	}
	if !ok {
		ui.Error("You did not accept the host key. Do not continue until you know the correct host key.")
		return ErrAborted
	}
	if err := s.ensureSecretsDir(); err != nil {
		return err
	}
	file := s.env.Paths.Secret(knownHostsFileName)
	if err := s.writeSecret(file, []byte(sftp.KnownHostsLine(d.Host, d.Port, key))); err != nil {
		return err
	}
	ui.Success("dbak pinned the host key in " + file + ".")
	return nil
}

func (s *session) testUpload() error {
	d := s.cfg.Destination
	client, err := s.env.DialSFTP(sftp.Config{
		Host:           d.Host,
		Port:           d.Port,
		Username:       d.Username,
		PrivateKeyFile: d.PrivateKeyFile,
		KnownHostsFile: d.KnownHostsFile,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	dir := path.Join(d.RemotePath, s.env.Hostname)
	if err := client.WriteCheck(dir); err != nil {
		return err
	}
	s.env.UI.Success("The test upload to " + dir + " passed.")
	return nil
}

func absolutePath(value string) error {
	if !path.IsAbs(value) {
		return errors.New("type a path that starts with /")
	}
	return nil
}

func (s *session) ensureSecretsDir() error {
	if err := s.env.Owner.MkdirAll(s.env.Paths.ConfigDir(), 0o750); err != nil {
		return err
	}
	return s.env.Owner.MkdirAll(s.env.Paths.SecretsDir(), 0o700)
}

func (s *session) writeSecret(file string, data []byte) error {
	return s.writeFile(file, data, 0o600)
}

func (s *session) writeFile(file string, data []byte, mode os.FileMode) error {
	if err := config.WriteFileAtomic(file, data, mode); err != nil {
		return err
	}
	return s.env.Owner.Apply(file, mode)
}
