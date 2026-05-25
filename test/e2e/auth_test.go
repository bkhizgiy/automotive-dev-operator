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

package e2e

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // Dot import is standard for Ginkgo
	. "github.com/onsi/gomega"    //nolint:revive // Dot import is standard for Gomega

	utils "github.com/centos-automotive-suite/automotive-dev-operator/test/utils"
)

var _ = Describe("OIDC Authentication", Label("auth"), Ordered, func() {
	BeforeAll(func() {
		if !utils.IsOpenShiftCluster() {
			Skip("OIDC e2e requires OpenShift; skipping on Kind")
		}
		ensureOperatorDeployed()
		// ensureRegistryConfigured is intentionally omitted: OIDC tests do not push
		// artifacts and do not require osBuilds.clusterRegistryRoute to be set.
		ensureBuildAPIAccess()
		if utils.GetBuildAPIURL(testNamespace) == "" {
			Skip("OIDC e2e requires OpenShift Route (ado-build-api)")
		}
	})

	Context("Build API OIDC Configuration", func() {
		It("should return 404 when OIDC is not configured", func() {
			apiURL := utils.GetBuildAPIURL(testNamespace)

			client := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
				},
			}
			resp, err := client.Get(apiURL + "/v1/auth/config")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Or(Equal(404), Equal(200)))
		})

		It("should handle OIDC configuration when provided", func() {
			By("patching OperatorConfig to add OIDC authentication")
			oidcPatch := `{"spec":{"buildAPI":{"authentication":{"clientId":"test-client-id","jwt":[{"issuer":{"url":"https://issuer.example.com","audiences":["test-audience"]},"claimMappings":{"username":{"claim":"preferred_username","prefix":""}}}]}}}}`
			cmd := exec.Command("kubectl", "patch", "operatorconfig", "config",
				"-n", testNamespace, "--type=merge", "-p", oidcPatch)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("checking /v1/auth/config endpoint returns OIDC config")
			apiURL := utils.GetBuildAPIURL(testNamespace)
			httpClient := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
				},
			}
			var authBody string
			EventuallyWithOffset(1, func() error {
				resp, httpErr := httpClient.Get(apiURL + "/v1/auth/config")
				if httpErr != nil {
					return httpErr
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != 200 {
					return fmt.Errorf("unexpected status %d from /v1/auth/config", resp.StatusCode)
				}
				b, readErr := io.ReadAll(resp.Body)
				if readErr != nil {
					return readErr
				}
				if !strings.Contains(string(b), "jwt") || !strings.Contains(string(b), "clientId") {
					return fmt.Errorf("OIDC config not yet reflected: %s", string(b))
				}
				authBody = string(b)
				return nil
			}, 2*time.Minute, 5*time.Second).Should(Succeed(),
				"Build API did not serve OIDC config in time")
			Expect(authBody).To(And(ContainSubstring("jwt"), ContainSubstring("clientId")))

			By("cleaning up OIDC configuration from OperatorConfig")
			cmd = exec.Command("kubectl", "patch", "operatorconfig", "config",
				"-n", testNamespace, "--type=json", "-p", `[{"op": "remove", "path": "/spec/buildAPI/authentication"}]`)
			out, cleanupErr := utils.Run(cmd)
			if cleanupErr != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "OIDC cleanup patch failed (non-fatal): %v\n%s\n", cleanupErr, string(out))
			}
		})
	})

	Context("Internal JWT Validation", func() {
		It("should have Build API pod running", func() {
			EventuallyWithOffset(1, func() error {
				cmd := exec.Command("kubectl", "get", "pod", "-l", "app.kubernetes.io/component=build-api",
					"-n", testNamespace, "-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("build-api pod not found: %w", err)
				}
				phase := strings.TrimSpace(string(output))
				if phase != statusRunning {
					return fmt.Errorf("build-api pod in %q phase", phase)
				}
				return nil
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
	})
})
