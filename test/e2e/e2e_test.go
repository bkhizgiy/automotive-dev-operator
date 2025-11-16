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
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/centos-automotive-suite/automotive-dev-operator/test/utils"
)

const namespace = "automotive-dev-operator-system"

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("deleting OperatorConfig resources")
		cmd := exec.Command("kubectl", "delete", "operatorconfig", "--all", "-n", namespace, "--timeout=30s")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--timeout=60s")
		_, _ = utils.Run(cmd)
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			var projectimage = "example.com/automotive-dev-operator:v0.0.1"

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
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
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
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
				if !contains(tasks, "build-automotive-image") {
					// Collect controller logs for debugging
					logCmd := exec.Command("kubectl", "logs", "-n", namespace, "-l", "control-plane=controller-manager", "--tail=50")
					logs, _ := utils.Run(logCmd)
					return fmt.Errorf("build-automotive-image task not found, got: %s\nController logs:\n%s", tasks, string(logs))
				}
				if !contains(tasks, "push-artifact-registry") {
					return fmt.Errorf("push-artifact-registry task not found, got: %s", tasks)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyTektonTasks, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying Tekton Pipeline is created")
			verifyTektonPipeline := func() error {
				cmd = exec.Command("kubectl", "get", "pipeline", "automotive-build-pipeline", "-n", namespace, "-o", "jsonpath={.metadata.name}")
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

			By("verifying WebUI deployment is created")
			verifyWebUIDeployment := func() error {
				cmd = exec.Command("kubectl", "get", "deployment", "ado-webui", "-n", namespace, "-o", "jsonpath={.status.availableReplicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "1" {
					return fmt.Errorf("webui deployment not available, replicas: %s", output)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyWebUIDeployment, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying WebUI ingress is created")
			verifyWebUIIngress := func() error {
				cmd = exec.Command("kubectl", "get", "ingress", "ado-webui", "-n", namespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "ado-webui" {
					return fmt.Errorf("webui ingress not found, got: %s", output)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyWebUIIngress, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying Build API deployment is created")
			verifyBuildAPIDeployment := func() error {
				cmd = exec.Command("kubectl", "get", "deployment", "ado-build-api", "-n", namespace, "-o", "jsonpath={.status.availableReplicas}")
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

			By("verifying Build API ingress is created")
			verifyBuildAPIIngress := func() error {
				cmd = exec.Command("kubectl", "get", "ingress", "ado-build-api", "-n", namespace, "-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "ado-build-api" {
					return fmt.Errorf("build-api ingress not found, got: %s", output)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyBuildAPIIngress, 2*time.Minute, 5*time.Second).Should(Succeed())

		})
	})
})

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
