package cli

import (
	"fmt"
	"strconv"

	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/archer"
	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/manifest"
	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/store"
	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/term/color"
	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/term/log"
	"github.com/aws/amazon-ecs-cli-v2/internal/pkg/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// DatabaseCreateOpts contains the fields to collect to create a database.
type DatabaseCreateOpts struct {
	appName string
	db      *archer.Database

	manifestPath string

	dbManager     archer.DatabaseManager
	secretManager archer.SecretsManager
	storeReader   storeReader

	ws archer.Workspace

	*GlobalOpts
}

// Validate returns an error if the values provided by the user are invalid.
func (o *DatabaseCreateOpts) Validate() error {
	if o.ProjectName() != "" {
		_, err := o.storeReader.GetProject(o.ProjectName())
		if err != nil {
			return err
		}
	}
	if o.appName != "" {
		_, err := o.storeReader.GetApplication(o.ProjectName(), o.appName)
		if err != nil {
			return err
		}
	}

	return nil
}

// Ask asks for fields that are required but not passed in.
func (o *DatabaseCreateOpts) Ask() error {
	if err := o.askProject(); err != nil {
		return err
	}
	if err := o.askAppName(); err != nil {
		return err
	}

	if err := o.askDatabaseName(); err != nil {
		return err
	}
	if err := o.askEngine(); err != nil {
		return err
	}
	if err := o.askUsername(); err != nil {
		return err
	}
	return o.askPassword()
}

// Execute creates the cluster.
func (o *DatabaseCreateOpts) Execute() error {
	project := o.GlobalOpts.ProjectName()
	o.manifestPath = o.ws.AppManifestFileName(o.appName)
	pwKey := fmt.Sprintf("/ecs-cli-v2/%s/applications/%s/secrets/database", project, o.appName)

	o.db.BackupRetentionPeriod = 7
	o.db.MinCapacity = 2
	o.db.MaxCapacity = 4

	mft, err := o.readManifest()
	if err != nil {
		return err
	}
	lbmft := mft.(*manifest.LBFargateManifest)
	if lbmft.Variables == nil {
		lbmft.Variables = make(map[string]string)
	}
	if lbmft.Environments == nil {
		lbmft.Environments = make(map[string]manifest.LBFargateConfig)
	}

	envs, err := o.storeReader.ListEnvironments(project)
	if err != nil {
		return err
	}

	for _, e := range envs {
		o.db.ClusterIdentifier = fmt.Sprintf("%s-%s-%s-%s", project, e.Name, o.appName, o.db.DatabaseName)

		output, err := o.dbManager.CreateDatabase(o.db)
		if err != nil {
			return err
		}

		env := lbmft.Environments[e.Name]
		if _, ok := lbmft.Environments[e.Name]; !ok {
			env = manifest.LBFargateConfig{
				ContainersConfig: manifest.ContainersConfig{
					Variables: make(map[string]string),
				},
			}
		}
		if env.ContainersConfig.Variables == nil {
			env.ContainersConfig.Variables = make(map[string]string)
		}
		env.ContainersConfig.Variables["DB_HOST"] = *output.Endpoint
		lbmft.Environments[e.Name] = env

		lbmft.Variables["DB_PORT"] = strconv.Itoa(int(*output.Port))
	}

	log.Successf("Created the database %s in %s under project %s.\n",
		color.HighlightUserInput(o.db.DatabaseName), color.HighlightResource(o.appName),
		color.HighlightResource(project))

	if _, err := o.secretManager.CreateSecret(pwKey, o.db.Password); err != nil {
		return err
	}

	log.Successf("Created a secret with the database password.\n")

	if lbmft.Secrets == nil {
		lbmft.Secrets = make(map[string]string)
	}
	lbmft.Variables["DB_NAME"] = o.db.DatabaseName
	lbmft.Variables["DB_USERNAME"] = o.db.Username
	lbmft.Secrets["DB_PASSWORD"] = pwKey

	lbmft.Database.Engine = o.db.Engine
	lbmft.Database.MinCapacity = int(o.db.MinCapacity)
	lbmft.Database.MaxCapacity = int(o.db.MaxCapacity)

	if err = o.writeManifest(lbmft); err != nil {
		return err
	}

	log.Successf("Saved the parameters of the database to the manifest.\n")
	return nil
}

func (o *DatabaseCreateOpts) readManifest() (archer.Manifest, error) {
	raw, err := o.ws.ReadFile(o.manifestPath)
	if err != nil {
		return nil, err
	}
	return manifest.UnmarshalApp(raw)
}

func (o *DatabaseCreateOpts) writeManifest(manifest *manifest.LBFargateManifest) error {
	manifestBytes, err := yaml.Marshal(manifest)
	_, err = o.ws.WriteFile(manifestBytes, o.manifestPath)
	return err
}

func (o *DatabaseCreateOpts) askProject() error {
	if o.ProjectName() != "" {
		return nil
	}
	projNames, err := o.retrieveProjects()
	if err != nil {
		return err
	}
	if len(projNames) == 0 {
		log.Infoln("There are no projects to select.")
	}
	proj, err := o.prompt.SelectOne(
		"Which project:",
		applicationShowProjectNameHelpPrompt,
		projNames,
	)
	if err != nil {
		return fmt.Errorf("selecting projects: %w", err)
	}
	o.projectName = proj

	return nil
}

func (o *DatabaseCreateOpts) askAppName() error {
	if o.appName != "" {
		return nil
	}
	appNames, err := o.retrieveApplications()
	if err != nil {
		return err
	}
	if len(appNames) == 0 {
		log.Infof("No applications found in project '%s'\n.", o.ProjectName())
		return nil
	}
	appName, err := o.prompt.SelectOne(
		fmt.Sprintf("Which app:"),
		"The app this secret belongs to.",
		appNames,
	)
	if err != nil {
		return fmt.Errorf("selecting applications for project %s: %w", o.ProjectName(), err)
	}
	o.appName = appName

	return nil
}

func (o *DatabaseCreateOpts) askDatabaseName() error {
	if o.db.DatabaseName != "" {
		return nil
	}

	name, err := o.prompt.Get(
		fmt.Sprintf("Database name:"),
		fmt.Sprintf(`The name of the database.`),
		validateApplicationName) // TODO update validation, for all asked-for variables

	if err != nil {
		return fmt.Errorf("failed to get database name: %w", err)
	}

	o.db.DatabaseName = name
	return nil
}

func (o *DatabaseCreateOpts) askEngine() error {
	if o.db.Engine != "" {
		return nil
	}

	engines := []string{"mysql", "postgresql"}
	engine, err := o.prompt.SelectOne(
		fmt.Sprintf("Which engine:"),
		"The type of engine for the database.",
		engines,
	)
	if err != nil {
		return fmt.Errorf("selecting engine: %w", err)
	}

	o.db.Engine = engine
	return nil
}

func (o *DatabaseCreateOpts) askUsername() error {
	if o.db.Username != "" {
		return nil
	}

	name, err := o.prompt.Get(
		fmt.Sprintf("Username:"),
		fmt.Sprintf(`The name of the master user.`),
		validateApplicationName)

	if err != nil {
		return fmt.Errorf("failed to get username: %w", err)
	}

	o.db.Username = name
	return nil
}

func (o *DatabaseCreateOpts) askPassword() error {
	if o.db.Password != "" {
		return nil
	}

	password, err := o.prompt.GetSecret(
		fmt.Sprintf("Password:"),
		fmt.Sprintf(`The password of the master user.`),
	)

	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	o.db.Password = password
	return nil
}

func (o *DatabaseCreateOpts) retrieveProjects() ([]string, error) {
	projs, err := o.storeReader.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	projNames := make([]string, len(projs))
	for ind, proj := range projs {
		projNames[ind] = proj.Name
	}
	return projNames, nil
}

func (o *DatabaseCreateOpts) retrieveApplications() ([]string, error) {
	apps, err := o.storeReader.ListApplications(o.ProjectName())
	if err != nil {
		return nil, fmt.Errorf("listing applications for project %s: %w", o.ProjectName(), err)
	}
	appNames := make([]string, len(apps))
	for ind, app := range apps {
		appNames[ind] = app.Name
	}
	return appNames, nil
}

// BuildDatabaseCreateCmd adds a serverless Aurora cluster.
func BuildDatabaseCreateCmd() *cobra.Command {
	opts := DatabaseCreateOpts{
		db: &archer.Database{},

		GlobalOpts: NewGlobalOpts(),
	}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Creates a serverless Aurora database.",
		Example: `
  /code $ ecs-preview database create`,
		PreRunE: runCmdE(func(cmd *cobra.Command, args []string) error {
			store, err := store.New()
			if err != nil {
				return fmt.Errorf("connect to environment datastore: %w", err)
			}
			ws, err := workspace.New()
			if err != nil {
				return fmt.Errorf("new workspace: %w", err)
			}
			opts.ws = ws
			opts.dbManager = store
			opts.secretManager = store
			opts.storeReader = store

			return nil
		}),
		RunE: runCmdE(func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if err := opts.Ask(); err != nil {
				return err
			}
			return opts.Execute()
		}),
	}

	cmd.Flags().StringVarP(&opts.appName, appFlag, appFlagShort, "", appFlagDescription)
	cmd.Flags().StringVarP(&opts.db.DatabaseName, "db-name", "d", "", "Name of the database.")
	cmd.Flags().StringVarP(&opts.db.Engine, "engine", "e", "", "Type of database; mysql or postgresql.")
	cmd.Flags().StringVarP(&opts.db.Username, "username", "u", "", "Name of the master user.")
	cmd.Flags().StringVarP(&opts.db.Password, "password", "s", "", "Password of the master user.")
	cmd.Flags().StringP(projectFlag, projectFlagShort, "dw-run" /* default */, projectFlagDescription)
	viper.BindPFlag(projectFlag, cmd.Flags().Lookup(projectFlag))

	return cmd
}