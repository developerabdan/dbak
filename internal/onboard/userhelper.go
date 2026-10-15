package onboard

import (
	"errors"
	"fmt"

	"github.com/developerabdan/dbak/internal/config"
	"github.com/developerabdan/dbak/internal/sqlhelper"
)

type HelperResult struct {
	User     string
	Password string
}

func RunUserHelper(ui UI, engineName, defaultHost string) (HelperResult, error) {
	if engineName == "" {
		var err error
		engineName, err = selectEngine(ui, config.EnginePostgres)
		if err != nil {
			return HelperResult{}, err
		}
	}
	req, err := askHelperRequest(ui, engineName, defaultHost)
	if err != nil {
		return HelperResult{}, err
	}
	req.Password, err = sqlhelper.GeneratePassword()
	if err != nil {
		return HelperResult{}, err
	}
	script, err := sqlhelper.Render(req)
	if err != nil {
		return HelperResult{}, err
	}
	ui.Heading("SQL for the backup user")
	for _, step := range script.Steps {
		ui.Info(step.Instruction)
		ui.Code(step.SQL)
	}
	for _, warning := range script.Warnings {
		ui.Warn(warning)
	}
	ui.Info("The SQL contains a generated password. dbak never writes the password to a log.")
	ran, err := ui.Confirm("I ran the SQL", "Select Yes after you run the SQL on the database server.", true)
	if err != nil {
		return HelperResult{}, err
	}
	if !ran {
		ui.Info("Run the SQL on the database server, then run dbak init again.")
		return HelperResult{}, ErrAborted
	}
	return HelperResult{User: req.User, Password: req.Password}, nil
}

func selectEngine(ui UI, current string) (string, error) {
	return ui.Select("Select the database engine.", "",
		options("PostgreSQL", config.EnginePostgres, "MySQL or MariaDB", config.EngineMySQL), current)
}

func askHelperRequest(ui UI, engineName, defaultHost string) (sqlhelper.Request, error) {
	variants := sqlhelper.Variants(engineName)
	opts := make([]Option, 0, len(variants))
	for _, v := range variants {
		opts = append(opts, Option{Label: v.Label, Value: v.ID})
	}
	variantID, err := ui.Select("Select the version of the database server.",
		"To see the version, run "+sqlhelper.VersionQuery+" on the server.", opts, variants[0].ID)
	if err != nil {
		return sqlhelper.Request{}, err
	}
	variant, _ := sqlhelper.VariantByID(variantID)
	req := sqlhelper.Request{Variant: variant}
	req.User, err = ui.Input("User name for the backup user", "", sqlhelper.DefaultUser, validName)
	if err != nil {
		return req, err
	}
	databases, err := ui.Input("Database names", "Type one or more database names. Put a comma between the names.", "", validNameList)
	if err != nil {
		return req, err
	}
	req.Databases = sqlhelper.SplitList(databases)
	switch {
	case variant == sqlhelper.Postgres13:
		schemas, err := ui.Input("Schema names", "Type the schemas in each database. Put a comma between the names.", sqlhelper.DefaultSchema, validNameList)
		if err != nil {
			return req, err
		}
		req.Schemas = sqlhelper.SplitList(schemas)
		req.OwnerRole, err = ui.Input("Owner role of the tables", "Type the role that makes the tables in these schemas.", sqlhelper.DefaultOwnerRole, validName)
		if err != nil {
			return req, err
		}
	case engineName == config.EngineMySQL:
		req.Host, err = askMySQLHost(ui, defaultHost)
		if err != nil {
			return req, err
		}
	}
	return req, nil
}

func askMySQLHost(ui UI, defaultHost string) (string, error) {
	for {
		host, err := ui.Input("Host that the backup user connects from",
			"Type the IP address of this server, as the MySQL server sees it.", defaultHost, validHost)
		if err != nil {
			return "", err
		}
		if !sqlhelper.IsWildcardHost(host) {
			return host, nil
		}
		ui.Warn("The host % lets the user connect from all hosts.")
		ok, err := ui.Confirm("Do you want to use the host %?", "Select No to type a different host.", false)
		if err != nil {
			return "", err
		}
		if ok {
			return host, nil
		}
	}
}

func validName(value string) error {
	if !config.ValidName(value) {
		return errors.New("use only letters, digits, and underscores")
	}
	return nil
}

func validNameList(value string) error {
	names := sqlhelper.SplitList(value)
	if len(names) == 0 {
		return errors.New("type one or more names")
	}
	for _, name := range names {
		if !config.ValidName(name) {
			return fmt.Errorf("the name %q is not valid. Use only letters, digits, and underscores", name)
		}
	}
	return nil
}

func validHost(value string) error {
	if !sqlhelper.ValidHost(value) {
		return errors.New("type an IP address, a host name, or %")
	}
	return nil
}
