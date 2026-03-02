package main

import (
	common "github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/common"
)

// sanitizeBuildName is kept in main package for existing tests.
func sanitizeBuildName(name string) string {
	return common.SanitizeBuildName(name)
}
