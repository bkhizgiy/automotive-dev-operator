package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/authcmd"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/catalog"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/clilog"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/container"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/image"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/workspace"
	"github.com/spf13/cobra"
)

var validOutputFormats = map[string]bool{
	"table": true,
	"json":  true,
	"yaml":  true,
	"yml":   true,
}

func fullVersion() string {
	v := version
	if v == "" {
		v = "dev"
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				v += " (" + s.Value[:7] + ")"
				break
			}
		}
	}
	return v
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "caib",
		Short:   "Cloud Automotive Image Builder",
		Version: fullVersion(),
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			f := strings.ToLower(strings.TrimSpace(outputFormat))
			if !validOutputFormats[f] {
				return fmt.Errorf("invalid output format %q (supported: table, json, yaml)", outputFormat)
			}
			return nil
		},
	}

	rootCmd.InitDefaultVersionFlag()
	rootCmd.SetVersionTemplate("caib version: {{.Version}}\n")

	rootCmd.PersistentFlags().BoolVar(
		&insecureSkipTLS,
		"insecure",
		envBool("CAIB_INSECURE"),
		"skip TLS certificate verification (insecure, for testing only; env: CAIB_INSECURE)",
	)
	rootCmd.PersistentFlags().StringVar(
		&outputFormat,
		"output-format",
		"table",
		"output format: table, json, yaml",
	)
	rootCmd.PersistentFlags().BoolVarP(
		&quiet,
		"quiet",
		"q",
		false,
		"suppress informational output (errors and structured data are still shown)",
	)

	cobra.OnInitialize(func() {
		clilog.SetQuiet(quiet)
	})

	state := newRuntimeState()
	handlers := state.newHandlers()

	rootCmd.AddCommand(
		image.NewImageCmd(state.imageOptions(handlers)),
		newLoginCmd(),
		newStatusCmd(),
		container.NewContainerCmd(),
		catalog.NewCatalogCmd(),
		authcmd.NewAuthCmd(),
		workspace.NewWorkspaceCmd(),
	)

	return rootCmd
}
