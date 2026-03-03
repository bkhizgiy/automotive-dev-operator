package main

import (
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/authcmd"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/catalog"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/container"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/image"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "caib",
		Short:   "Cloud Automotive Image Builder",
		Version: version,
	}

	rootCmd.InitDefaultVersionFlag()
	rootCmd.SetVersionTemplate("caib version: {{.Version}}\n")

	rootCmd.PersistentFlags().BoolVar(
		&insecureSkipTLS,
		"insecure",
		envBool("CAIB_INSECURE"),
		"skip TLS certificate verification (insecure, for testing only; env: CAIB_INSECURE)",
	)
	state := newRuntimeState()
	handlers := state.newHandlers()

	rootCmd.AddCommand(
		image.NewImageCmd(state.imageOptions(handlers)),
		newLoginCmd(),
		container.NewContainerCmd(),
		catalog.NewCatalogCmd(),
		authcmd.NewAuthCmd(),
	)

	return rootCmd
}

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [server-url]",
		Short: "Save server endpoint and authenticate for subsequent commands",
		Long: `Login saves the Build API server URL locally (~/.caib/cli.json) so you do not need
to pass --server or set CAIB_SERVER for later commands. If the server uses OIDC,
this command also performs authentication and caches the token.

Example:
  caib login https://build-api.my-cluster.example.com`,
		Args: cobra.ExactArgs(1),
		Run:  runLogin,
	}
}
