/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package catalog

import (
	"github.com/spf13/cobra"
)

var (
	serverURL    string
	authToken    string
	namespace    string
	outputFormat string
)

// NewCatalogCmd creates the catalog command with subcommands
func NewCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Manage the automotive OS image catalog",
		Long:  `Commands for browsing, publishing, and managing images in the catalog.`,
	}

	// Add subcommands
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newGetCmd())
	cmd.AddCommand(newPublishCmd())
	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newRemoveCmd())
	cmd.AddCommand(newVerifyCmd())

	return cmd
}

// addCommonFlags adds common flags to catalog subcommands
func addCommonFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&serverURL, "server", "", "REST API server base URL (env: CAIB_SERVER)")
	cmd.Flags().StringVar(&authToken, "token", "", "Bearer token for authentication (env: CAIB_TOKEN)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json, yaml)")
}
