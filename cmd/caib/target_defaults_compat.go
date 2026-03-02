package main

import (
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/buildcmd"
	buildapitypes "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi"
	"github.com/spf13/cobra"
)

// applyTargetDefaults is kept in main package for existing tests.
func applyTargetDefaults(cmd *cobra.Command, config *buildapitypes.OperatorConfigResponse, req *buildapitypes.BuildRequest) {
	buildcmd.ApplyTargetDefaults(cmd, config, req)
}
