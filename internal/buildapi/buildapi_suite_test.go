// Package buildapi_test provides test suite for the build API server.
package buildapi

import (
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // Dot import is standard for Ginkgo
	. "github.com/onsi/gomega"    //nolint:revive // Dot import is standard for Gomega
)

func TestBuildapi(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BuildAPI Suite")
}
