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
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // Dot import is standard for Ginkgo
	. "github.com/onsi/gomega"    //nolint:revive // Dot import is standard for Gomega

	utils "github.com/centos-automotive-suite/automotive-dev-operator/test/utils"
)

const (
	namespace                      = "automotive-dev-operator-system"
	archARM64                      = "arm64"
	aarch64                        = "aarch64"
	statusRunning                  = "Running"
	tektonTaskPushArtifactRegistry = "push-artifact-registry"
	artifactImageRepo              = "automotive-os-test1"
	artifactImageName              = artifactImageRepo + ":latest"
	kindPushNamespace              = "myorg"
)

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {

		By("removing manager namespace")
		utils.CleanupNamespace(namespace)

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("removing manager namespace")
		utils.CleanupNamespace(namespace)
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			var projectimage = "example.com/automotive-dev-operator:v0.0.1"
			deployedImage := utils.PrepareOperatorImage(projectimage, namespace)

			By("installing CRDs")
			cmd := exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the operator")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", deployedImage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the operator pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=operator",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("operator"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != statusRunning {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())

			By("creating OperatorConfig resource")
			cmd = exec.Command("kubectl", "apply", "-f", "config/samples/automotive_v1_operatorconfig.yaml")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("verifying Tekton Tasks are created")
			verifyTektonTasks := func() error {
				cmd = exec.Command("kubectl", "get", "tasks", "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				tasks := string(output)
				if !strings.Contains(tasks, "build-automotive-image") {
					// Collect controller logs for debugging
					logCmd := exec.Command("kubectl", "logs", "-n", namespace, "-l", "control-plane=operator", "--tail=50")
					logs, _ := utils.Run(logCmd)
					return fmt.Errorf("build-automotive-image task not found, got: %s\nController logs:\n%s", tasks, string(logs))
				}
				if !strings.Contains(tasks, tektonTaskPushArtifactRegistry) {
					return fmt.Errorf("%s task not found, got: %s", tektonTaskPushArtifactRegistry, tasks)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyTektonTasks, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying Tekton Pipeline is created")
			verifyTektonPipeline := func() error {
				cmd = exec.Command("kubectl", "get", "pipeline", "automotive-build-pipeline",
					"-n", namespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "automotive-build-pipeline" {
					return fmt.Errorf("automotive-build-pipeline not found, got: %s", output)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyTektonPipeline, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying Build API deployment is created")
			verifyBuildAPIDeployment := func() error {
				cmd = exec.Command("kubectl", "get", "deployment", "ado-build-api",
					"-n", namespace, "-o", "jsonpath={.status.availableReplicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "1" {
					return fmt.Errorf("build-api deployment not available, replicas: %s", output)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyBuildAPIDeployment, 3*time.Minute, 5*time.Second).Should(Succeed())
		})

		Context("caib image build", func() {
			var portForwardCmd *exec.Cmd
			var caibServer string
			var registryHost string
			var arch string
			var caibToken string
			var openShiftCluster bool
			var caibEnv []string
			var projectimage = "automotive-dev-operator:test"
			var caibBuildTimeout = 45 * time.Minute // max timeout for caib builds

			BeforeAll(func() {
				registryHost = os.Getenv("REGISTRY_HOST")
				if registryHost == "" {
					Skip("REGISTRY_HOST not set; caib build tests require a local registry")
				}

				arch = os.Getenv("ARCH")
				if arch == "" {
					unameCmd := exec.Command("uname", "-m")
					unameOutput, _ := utils.Run(unameCmd)
					switch strings.TrimSpace(string(unameOutput)) {
					case archARM64, aarch64:
						arch = archARM64
					default:
						arch = "amd64"
					}
				}

				By("ensuring namespace has privileged PSA labels")
				cmd := exec.Command("kubectl", "label", "namespace", namespace,
					"pod-security.kubernetes.io/enforce=privileged", "--overwrite")
				_, err := utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
				cmd = exec.Command("kubectl", "label", "namespace", namespace,
					"pod-security.kubernetes.io/audit=privileged", "--overwrite")
				_, _ = utils.Run(cmd)
				cmd = exec.Command("kubectl", "label", "namespace", namespace,
					"pod-security.kubernetes.io/warn=privileged", "--overwrite")
				_, _ = utils.Run(cmd)

				deployedImage := utils.PrepareOperatorImage(projectimage, namespace)

				By("installing CRDs")
				cmd = exec.Command("make", "install")
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("deploying the operator")
				cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", deployedImage))
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("waiting for operator deployment to be available")
				cmd = exec.Command("kubectl", "wait", "--for=condition=available",
					"--timeout=10m", "deployment/ado-operator", "-n", namespace)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("waiting for Build API deployment to be available")
				cmd = exec.Command("kubectl", "apply", "-f",
					"config/samples/automotive_v1_operatorconfig.yaml")
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("waiting for Build API deployment to be available")
				cmd = exec.Command("kubectl", "wait", "--for=condition=available",
					"--timeout=8m", "deployment/ado-build-api", "-n", namespace)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("patching OperatorConfig for registry")
				patch := fmt.Sprintf(`{"spec":{"osBuilds":{"clusterRegistryRoute":"%s:5000","insecureRegistry":true}}}`, registryHost)
				cmd = exec.Command("kubectl", "patch", "operatorconfig", "config",
					"-n", namespace, "--type=merge",
					"-p", patch)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("waiting for " + tektonTaskPushArtifactRegistry + " Task to be created")
				waitForPushTask := func() error {
					taskCmd := exec.Command("kubectl", "get", "task", tektonTaskPushArtifactRegistry, "-n", namespace)
					_, taskErr := utils.Run(taskCmd)
					return taskErr
				}
				EventuallyWithOffset(1, waitForPushTask, 2*time.Minute, 5*time.Second).Should(Succeed(),
					tektonTaskPushArtifactRegistry+" Task was not created in time")

				openShiftCluster = utils.IsOpenShiftCluster()
				if openShiftCluster {
					By("patching " + tektonTaskPushArtifactRegistry + " Task for OpenShift (OCI referrers compat)")
					cmd = exec.Command("kubectl", "annotate", "task", tektonTaskPushArtifactRegistry,
						"-n", namespace, "automotive.sdv.cloud.redhat.com/unmanaged=true", "--overwrite")
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					err = utils.PatchTektonTaskStep(namespace, tektonTaskPushArtifactRegistry, 0,
						map[string]string{
							"--image-spec v1.1":                    "--image-spec v1.0",
							`oras" attach "${ORAS_EXTRA_ARGS[@]}"`: `oras" attach --distribution-spec v1.1-referrers-tag "${ORAS_EXTRA_ARGS[@]}"`,
						}, nil)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					// Pre-create the ImageStream so the internal registry can handle manifest
					// HEAD checks properly. Without it the registry returns 500 instead of
					// 404 for missing manifests, which causes oras push to abort.
					By("pre-creating artifact ImageStream on OpenShift")
					cmd = exec.Command("oc", "create", "imagestream", artifactImageRepo, "-n", namespace)
					_, _ = utils.Run(cmd)

					By("waiting for Build API route")
					EventuallyWithOffset(1, func() string {
						caibServer = utils.GetBuildAPIURL(namespace)
						return caibServer
					}, 2*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

					By("waiting for Build API route to respond")
					httpClient := &http.Client{
						Timeout: 2 * time.Second,
						Transport: &http.Transport{
							TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
						},
					}
					waitForBuildAPI := func() error {
						resp, httpErr := httpClient.Get(caibServer + "/v1/healthz")
						if httpErr != nil {
							return httpErr
						}
						_ = resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							return nil
						}
						return fmt.Errorf("unexpected status %d from Build API /v1/healthz", resp.StatusCode)
					}
					EventuallyWithOffset(1, waitForBuildAPI, 2*time.Minute, 5*time.Second).Should(Succeed(),
						"Build API route did not become ready")
				} else {
					// Kind-specific workaround: runAsUser: 0 is needed because plain
					// Kubernetes lacks OpenShift's SCC (Security Context Constraints) that
					// would grant the push step access to root-owned build artifacts in the
					// shared workspace. The --insecure flag for insecure registries is now
					// handled natively via OperatorConfig.spec.osBuilds.insecureRegistry.
					By("patching " + tektonTaskPushArtifactRegistry + " Task for Kind (runAsUser 0)")
					cmd = exec.Command("kubectl", "annotate", "task", tektonTaskPushArtifactRegistry,
						"-n", namespace, "automotive.sdv.cloud.redhat.com/unmanaged=true", "--overwrite")
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					err = utils.PatchTektonTaskStep(namespace, tektonTaskPushArtifactRegistry, 0,
						nil,
						map[string]any{"securityContext": map[string]any{"runAsUser": 0}})
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					By("ensuring port 8080 is free before starting port-forward")
					if conn, dialErr := net.DialTimeout("tcp", "localhost:8080", 500*time.Millisecond); dialErr == nil {
						_ = conn.Close()
						Fail("port 8080 is already in use; cannot set up port-forward to Build API")
					}

					By("setting up port-forward to Build API")
					portForwardCmd = exec.Command("kubectl", "port-forward",
						"-n", namespace, "svc/ado-build-api", "8080:8080")
					err = portForwardCmd.Start()
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					By("waiting for Build API to respond on port-forward")
					httpClient := &http.Client{Timeout: 2 * time.Second}
					waitForBuildAPI := func() error {
						resp, httpErr := httpClient.Get("http://localhost:8080/v1/healthz")
						if httpErr != nil {
							return httpErr
						}
						_ = resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							return nil
						}
						return fmt.Errorf("unexpected status %d from Build API /v1/healthz", resp.StatusCode)
					}
					EventuallyWithOffset(1, waitForBuildAPI, 30*time.Second, 1*time.Second).Should(Succeed(),
						"Build API on localhost:8080 did not become ready")
					caibServer = "http://localhost:8080"
				}

				By("creating service account and token")
				cmd = exec.Command("kubectl", "create", "serviceaccount", "caib",
					"-n", namespace, "--dry-run=client", "-o", "yaml")
				saYAML, saErr := utils.Run(cmd)
				ExpectWithOffset(1, saErr).NotTo(HaveOccurred())
				cmd = exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(string(saYAML))
				_, saErr = utils.Run(cmd)
				ExpectWithOffset(1, saErr).NotTo(HaveOccurred())

				By("creating caib token")
				cmd = exec.Command("kubectl", "create", "token", "caib",
					"-n", namespace, "--duration=1h")
				tokenOutput, tokenErr := utils.Run(cmd)
				ExpectWithOffset(1, tokenErr).NotTo(HaveOccurred())
				caibToken = strings.TrimSpace(string(tokenOutput))
				ExpectWithOffset(1, caibToken).NotTo(BeEmpty(), "CAIB_TOKEN must not be empty")

				By("setting caib environment variables")
				caibEnv = append([]string{}, os.Environ()...)
				setEnv := func(key, value string) {
					prefix := key + "="
					filtered := caibEnv[:0]
					for _, entry := range caibEnv {
						if !strings.HasPrefix(entry, prefix) {
							filtered = append(filtered, entry)
						}
					}
					caibEnv = append(filtered, prefix+value)
				}
				setEnv("CAIB_TOKEN", caibToken)
				setEnv("CAIB_SERVER", caibServer)
				if openShiftCluster {
					setEnv("CAIB_INSECURE", "true")
				}

				By("setting registry credentials")
				registryUsername := os.Getenv("REGISTRY_USERNAME")
				registryPassword := os.Getenv("REGISTRY_PASSWORD")
				if openShiftCluster {
					// Registry tokens must be passed only via process env (caib / REGISTRY_* for
					// subprocesses) or stdin (e.g. podman --password-stdin in test utils), never
					// as CLI args, so Ginkgo/Run logs cannot leak them.
					ocUser, ocErr := utils.Run(exec.Command("oc", "whoami"))
					ExpectWithOffset(1, ocErr).NotTo(HaveOccurred())
					ocToken, ocErr := utils.Run(exec.Command("oc", "whoami", "-t"))
					ExpectWithOffset(1, ocErr).NotTo(HaveOccurred())
					registryUsername = strings.TrimSpace(string(ocUser))
					registryPassword = strings.TrimSpace(string(ocToken))
				} else if registryUsername == "" {
					registryUsername = "kind"
					registryPassword = "kind"
				}
				if registryUsername != "" {
					setEnv("REGISTRY_USERNAME", registryUsername)
				}
				if registryPassword != "" {
					setEnv("REGISTRY_PASSWORD", registryPassword)
				}
			})

			AfterAll(func() {
				if portForwardCmd != nil {
					if portForwardCmd.Process != nil {
						_ = portForwardCmd.Process.Kill()
					}
					_ = portForwardCmd.Wait()
				}
			})

			verifyCaibList := func(caibBuildName string) {
				listCmd := utils.NewCaibCommand(context.Background(), caibEnv,
					"image", "list")
				listOutput, listErr := utils.Run(listCmd)
				ExpectWithOffset(2, listErr).NotTo(HaveOccurred())
				lines := strings.Split(string(listOutput), "\n")
				found := false
				for _, line := range lines {
					if strings.Contains(line, caibBuildName) {
						ExpectWithOffset(2, line).To(ContainSubstring("Completed"))
						found = true
						break
					}
				}
				ExpectWithOffset(2, found).To(BeTrue(),
					fmt.Sprintf("build %q not found in caib list output:\n%s", caibBuildName, string(listOutput)))
			}

			It("should build a container image via caib", func() {
				containerBuildName := "e2e-test-build-image"

				type buildResult struct {
					output []byte
					err    error
				}

				ctx, cancel := context.WithTimeout(context.Background(), caibBuildTimeout)
				defer cancel()

				containerCh := make(chan buildResult, 1)

				pushNamespace := kindPushNamespace
				if openShiftCluster {
					pushNamespace = namespace
				}

				By("launching bootc container build")
				go func() {
					cmd := utils.NewCaibCommand(ctx, caibEnv,
						"image", "build-dev",
						"test/config/test-manifest.aib.yml",
						"--name", containerBuildName,
						"--arch", arch,
						"--push", fmt.Sprintf("%s:5000/%s/%s", registryHost, pushNamespace, artifactImageName),
						"--follow")
					out, err := utils.RunSafe(cmd)
					containerCh <- buildResult{output: out, err: err}
				}()

				select {
				case cr := <-containerCh:
					By("verifying container build completed successfully")
					_, _ = fmt.Fprintf(GinkgoWriter, "\n--- caib container build (%s) ---\n%s\n", containerBuildName, string(cr.output))
					ExpectWithOffset(1, cr.err).NotTo(HaveOccurred(),
						fmt.Sprintf("container build failed:\n%sError: %v\n", string(cr.output), cr.err))
				case <-ctx.Done():
					Fail(fmt.Sprintf("caib build did not complete within %v", caibBuildTimeout))
				}

				By("verifying container build appears in caib list")
				verifyCaibList(containerBuildName)
			})
		})
	})
})

var _ = Describe("OIDC Authentication", Ordered, func() {
	var oidcSuiteCreatedNamespace bool

	BeforeAll(func() {
		var err error
		var projectimage = "example.com/automotive-dev-operator:v0.0.1"

		if !utils.IsOpenShiftCluster() {
			Skip("OIDC e2e requires OpenShift (Route CRD); skipping on kind")
		}
		oidcSuiteCreatedNamespace = true

		// The controller suite's AfterAll may have just deleted this namespace.
		// Wait for it to be fully gone before creating it again — otherwise the
		// registry storage backend is in a torn-down state and podman push gets 500.
		By("waiting for namespace to be fully gone before creating it")
		utils.CleanupNamespace(namespace)

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)

		deployedImage := utils.PrepareOperatorImage(projectimage, namespace)

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("deploying the operator")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", deployedImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("validating that the operator pod is running")
		verifyControllerUp := func() error {
			cmd = exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=operator",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)
			podOutput, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			podNames := utils.GetNonEmptyLines(string(podOutput))
			if len(podNames) != 1 {
				return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
			}
			cmd = exec.Command("kubectl", "get",
				"pods", podNames[0], "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			status, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			if string(status) != statusRunning {
				return fmt.Errorf("controller pod in %s status", status)
			}
			return nil
		}
		Eventually(verifyControllerUp, time.Minute, time.Second).Should(Succeed())

		By("creating baseline OperatorConfig without OIDC")
		cmd = exec.Command("kubectl", "apply", "-f", "config/samples/automotive_v1_operatorconfig.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for Build API deployment")
		verifyBuildAPIDeployment := func() error {
			cmd = exec.Command("kubectl", "get", "deployment", "ado-build-api",
				"-n", namespace, "-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(output)) != "1" {
				return fmt.Errorf("build-api deployment not available, replicas: %s", output)
			}
			return nil
		}
		Eventually(verifyBuildAPIDeployment, 3*time.Minute, 5*time.Second).Should(Succeed())

		if utils.GetBuildAPIURL(namespace) == "" {
			Skip("OIDC e2e requires OpenShift Route (ado-build-api); skipping on kind")
		}
	})

	AfterAll(func() {
		if !oidcSuiteCreatedNamespace {
			return
		}
		By("deleting OperatorConfig so namespace can terminate cleanly")
		cmd := exec.Command("kubectl", "delete", "operatorconfig", "--all", "-n", namespace, "--timeout=30s")
		_, _ = utils.Run(cmd)

		By("waiting for OperatorConfig to be fully removed (finalizer cleared)")
		waitForOperatorConfigGone := func() error {
			cmd := exec.Command("kubectl", "get", "operatorconfig", "-n", namespace, "-o", "name")
			output, err := utils.Run(cmd)
			if err != nil {
				return nil
			}
			if strings.TrimSpace(string(output)) == "" {
				return nil
			}
			return fmt.Errorf("operatorconfig still present")
		}
		Eventually(waitForOperatorConfigGone, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("removing manager namespace")
		utils.CleanupNamespace(namespace)
	})

	Context("Build API OIDC Configuration", func() {
		It("should return 404 when OIDC is not configured", func() {
			By("getting Build API URL")
			apiURL := utils.GetBuildAPIURL(namespace)

			By("checking /v1/auth/config endpoint returns 404 when OIDC not configured")
			client := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
				},
			}
			resp, err := client.Get(apiURL + "/v1/auth/config")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = resp.Body.Close()
			}()
			// Should return 404 or 200 with empty JWT array
			statusCode := resp.StatusCode
			Expect(statusCode).To(Or(Equal(404), Equal(200)))
		})

		It("should handle OIDC configuration when provided", func() {
			By("patching OperatorConfig to add OIDC authentication")
			// Use merge-patch so existing spec fields (osBuilds, etc.) are preserved.
			// A full kubectl apply with only buildAPI would strip osBuilds, triggering
			// cleanupOSBuilds in the reconciler which deletes the Route and deployment.
			oidcPatch := `{"spec":{"buildAPI":{"authentication":{"clientId":"test-client-id","jwt":[{"issuer":{"url":"https://issuer.example.com","audiences":["test-audience"]},"claimMappings":{"username":{"claim":"preferred_username","prefix":""}}}]}}}}`
			cmd := exec.Command("kubectl", "patch", "operatorconfig", "config",
				"-n", namespace, "--type=merge", "-p", oidcPatch)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("checking /v1/auth/config endpoint returns OIDC config")
			apiURL := utils.GetBuildAPIURL(namespace)
			if apiURL == "" {
				Skip("Build API Route not found (OpenShift required)")
			}
			httpClient := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
				},
			}
			// The Build API pod restarts after the OperatorConfig patch; poll until the
			// new OIDC config is served (up to 2 minutes).
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
				"-n", namespace, "--type=json", "-p", `[{"op": "remove", "path": "/spec/buildAPI/authentication"}]`)
			_, _ = utils.Run(cmd)
		})
	})

	Context("Internal JWT Validation", func() {
		It("should have Build API pod running", func() {
			By("verifying Build API pod is running")
			EventuallyWithOffset(1, func() error {
				cmd := exec.Command("kubectl", "get", "pod", "-l", "app.kubernetes.io/component=build-api",
					"-n", namespace, "-o", "jsonpath={.items[0].status.phase}")
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
