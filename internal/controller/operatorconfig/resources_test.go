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

package operatorconfig

import (
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	corev1 "k8s.io/api/core/v1"
)

func TestResources(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OperatorConfig Resources Suite")
}

var _ = Describe("OperatorConfig Resources", func() {
	var r *OperatorConfigReconciler

	BeforeEach(func() {
		r = &OperatorConfigReconciler{}
	})

	Describe("buildBuildAPIDeployment", func() {
		It("should use ado-operator service account", func() {
			deployment := r.buildBuildAPIDeployment(false)
			Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal("ado-operator"))
		})

		It("should use ado-operator service account on OpenShift", func() {
			deployment := r.buildBuildAPIDeployment(true)
			Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal("ado-operator"))
		})
	})

	Describe("buildBuildAPIContainers", func() {
		It("should not include oauth-proxy on non-OpenShift", func() {
			containers := r.buildBuildAPIContainers(false)
			Expect(containers).To(HaveLen(1))
			Expect(containers[0].Name).To(Equal("build-api"))
		})

		It("should include oauth-proxy on OpenShift with ado-operator SA", func() {
			containers := r.buildBuildAPIContainers(true)
			Expect(containers).To(HaveLen(2))

			oauthProxy := containers[1]
			Expect(oauthProxy.Name).To(Equal("oauth-proxy"))
			Expect(oauthProxy.Args).To(ContainElement("--openshift-service-account=ado-operator"))
		})

		It("should not reference ado-controller-manager in oauth-proxy args", func() {
			containers := r.buildBuildAPIContainers(true)
			for _, arg := range containers[1].Args {
				Expect(arg).NotTo(ContainSubstring("controller-manager"))
			}
		})
	})

	Describe("buildBuildControllerDeployment", func() {
		It("should use ado-build-controller service account", func() {
			deployment := r.buildBuildControllerDeployment()
			Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal("ado-build-controller"))
		})

		It("should run in build mode", func() {
			deployment := r.buildBuildControllerDeployment()
			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Args).To(ContainElement("--mode=build"))
		})

		It("should set pod-level RunAsNonRoot", func() {
			deployment := r.buildBuildControllerDeployment()
			podSec := deployment.Spec.Template.Spec.SecurityContext
			Expect(podSec).NotTo(BeNil())
			Expect(podSec.RunAsNonRoot).NotTo(BeNil())
			Expect(*podSec.RunAsNonRoot).To(BeTrue())
		})

		It("should drop all capabilities and disallow privilege escalation", func() {
			deployment := r.buildBuildControllerDeployment()
			container := deployment.Spec.Template.Spec.Containers[0]
			sec := container.SecurityContext
			Expect(sec).NotTo(BeNil())
			Expect(sec.AllowPrivilegeEscalation).NotTo(BeNil())
			Expect(*sec.AllowPrivilegeEscalation).To(BeFalse())
			Expect(sec.Capabilities).NotTo(BeNil())
			Expect(sec.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))
		})
	})
})
