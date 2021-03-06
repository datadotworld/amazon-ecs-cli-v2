package cli

import (
	"fmt"
	"strings"

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

// SecretAddOpts contains the fields to collect to create a secret.
type SecretAddOpts struct {
	appName     string
	envName     string
	secretName  string
	secretValue string

	manifestPath string

	secretManager archer.SecretsManager
	storeReader   storeReader

	ws archer.Workspace

	*GlobalOpts
}

// Validate returns an error if the values provided by the user are invalid.
func (o *SecretAddOpts) Validate() error {
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
	if o.envName != "" {
		if _, err := o.storeReader.GetEnvironment(o.ProjectName(), o.envName); err != nil {
			return err
		}
	}
	return nil
}

// Ask asks for fields that are required but not passed in.
func (o *SecretAddOpts) Ask() error {
	if err := o.askProject(); err != nil {
		return err
	}
	if err := o.askAppName(); err != nil {
		return err
	}

	if err := o.askSecretName(); err != nil {
		return err
	}
	return o.askSecretValue()
}

// Execute encrypts the secret.
func (o *SecretAddOpts) Execute() error {
	o.manifestPath = o.ws.AppManifestFileName(o.appName)
	mft, err := o.readManifest()
	if err != nil {
		return err
	}

	name := strings.ToLower(o.secretName)
	name = strings.ReplaceAll(name, "_", "-")

	key := fmt.Sprintf("/ecs-cli-v2/%s/applications/%s/secrets/%s", o.GlobalOpts.ProjectName(),
		o.appName, name)
	if o.envName != "" {
		key = fmt.Sprintf("%s-%s", key, o.envName)
	}

	if _, err := o.secretManager.CreateSecret(key, o.secretValue); err != nil {
		return err
	}

	log.Successf("Created/updated %s in %s under project %s.\n", color.HighlightUserInput(o.secretName),
		color.HighlightResource(o.appName), color.HighlightResource(o.GlobalOpts.ProjectName()))

	// save the secret to the manifest
	lbmft := mft.(*manifest.LBFargateManifest)

	if o.envName == "" {
		if lbmft.Secrets == nil {
			lbmft.Secrets = make(map[string]string)
		}
		lbmft.Secrets[o.secretName] = key
	} else {
		if lbmft.Environments == nil {
			lbmft.Environments = make(map[string]manifest.LBFargateConfig)
		}
		if _, ok := lbmft.Environments[o.envName]; !ok {
			lbmft.Environments[o.envName] = manifest.LBFargateConfig{}
		}
		if lbmft.Environments[o.envName].Secrets == nil {
			envConf := lbmft.Environments[o.envName]
			envConf.Secrets = make(map[string]string)
			lbmft.Environments[o.envName] = envConf
		}
		lbmft.Environments[o.envName].Secrets[o.secretName] = key
	}

	if err = o.writeManifest(lbmft); err != nil {
		return err
	}

	log.Successf("Saved the secret %s to the manifest.\n", color.HighlightUserInput(o.secretName))
	return nil
}

func (o *SecretAddOpts) readManifest() (archer.Manifest, error) {
	raw, err := o.ws.ReadFile(o.manifestPath)
	if err != nil {
		return nil, err
	}
	return manifest.UnmarshalApp(raw)
}

func (o *SecretAddOpts) writeManifest(mft *manifest.LBFargateManifest) error {
	manifestBytes, err := yaml.Marshal(mft)
	_, err = o.ws.WriteFile(manifestBytes, o.manifestPath)
	return err
}

func (o *SecretAddOpts) askProject() error {
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

func (o *SecretAddOpts) askAppName() error {
	if o.appName != "" {
		return nil
	}
	appNames, err := o.workspaceAppNames()
	if err != nil {
		return err
	}
	if len(appNames) == 0 {
		log.Infof("No applications found in project '%s'\n.", o.ProjectName())
		return nil
	}
	if len(appNames) == 1 {
		o.appName = appNames[0]
		log.Infof("Found the app: %s\n", color.HighlightUserInput(o.appName))
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

func (o *SecretAddOpts) askSecretName() error {
	if o.secretName != "" {
		return nil
	}

	name, err := o.prompt.Get(
		fmt.Sprintf("Secret name (e.g. MY_SECRET):"),
		fmt.Sprintf(`The name that will uniquely identify your secret within your app.`),
		validateEnvVarName)

	if err != nil {
		return fmt.Errorf("failed to get secret name: %w", err)
	}

	o.secretName = name
	return nil
}

func (o *SecretAddOpts) askSecretValue() error {
	if o.secretValue != "" {
		return nil
	}

	secret, err := o.prompt.GetSecret(
		fmt.Sprintf("Value to encrypt:"),
		fmt.Sprintf(`The value to be encrypted and accessed by the app.`),
	)

	if err != nil {
		return fmt.Errorf("failed to get secret value: %w", err)
	}

	o.secretValue = secret
	return nil
}

func (o *SecretAddOpts) retrieveProjects() ([]string, error) {
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

func (o *SecretAddOpts) workspaceAppNames() ([]string, error) {
	apps, err := o.ws.Apps()
	if err != nil {
		return nil, fmt.Errorf("get applications in the workspace: %w", err)
	}
	var names []string
	for _, app := range apps {
		names = append(names, app.AppName())
	}
	return names, nil
}

// BuildSecretAddCmd adds a secret.
func BuildSecretAddCmd() *cobra.Command {
	opts := SecretAddOpts{
		GlobalOpts: NewGlobalOpts(),
	}
	cmd := &cobra.Command{
		Use:     "add",
		Aliases: []string{"create"},
		Short:   "Adds a secret.",
		Example: `
  /code $ dw_run.sh secret add -n secret-name
`,
		PreRunE: runCmdE(func(cmd *cobra.Command, args []string) error {
			ssmStore, err := store.New()
			if err != nil {
				return fmt.Errorf("connect to environment datastore: %w", err)
			}
			ws, err := workspace.New()
			if err != nil {
				return fmt.Errorf("new workspace: %w", err)
			}
			opts.ws = ws
			opts.storeReader = ssmStore
			opts.secretManager = ssmStore

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
	cmd.Flags().StringVarP(&opts.envName, envFlag, envFlagShort, "", envFlagDescription)
	cmd.Flags().StringVarP(&opts.secretName, "secret-name", "n", "", "Name of the secret, e.g. MY_SECRET.")
	cmd.Flags().StringVarP(&opts.secretValue, "secret-value", "v", "", "Value to encrypt.")
	cmd.Flags().StringP(projectFlag, projectFlagShort, "dw-run" /* default */, projectFlagDescription)
	viper.BindPFlag(projectFlag, cmd.Flags().Lookup(projectFlag))

	return cmd
}
