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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ImageBuildSpec defines the desired state of ImageBuild
// +kubebuilder:printcolumn:name="StorageClass",type=string,JSONPath=`.spec.storageClass`
type ImageBuildSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Distro specifies the distribution to build for (e.g., "cs9")
	Distro string `json:"distro,omitempty"`

	// Target specifies the build target (e.g., "qemu")
	Target string `json:"target,omitempty"`

	// Architecture specifies the target architecture
	Architecture string `json:"architecture,omitempty"`

	// ExportFormat specifies the output format (image, qcow2)
	ExportFormat string `json:"exportFormat,omitempty"`

	// Mode specifies the build mode (package, image)
	Mode string `json:"mode,omitempty"`

	// StorageClass is the name of the storage class to use for the build PVC
	StorageClass string `json:"storageClass,omitempty"`

	// AutomativeOSBuildImage specifies the image to use for building
	AutomativeOSBuildImage string `json:"automativeOSBuildImage,omitempty"`

	// MppConfigMap specifies the name of the ConfigMap containing the MPP configuration
	MppConfigMap string `json:"mppConfigMap,omitempty"`

	// Publishers defines where to publish the built artifacts
	Publishers *Publishers `json:"publishers,omitempty"`
}

// Publishers defines the configuration for artifact publishing
type Publishers struct {
	// Registry configuration for publishing to an OCI registry
	Registry *RegistryPublisher `json:"registry,omitempty"`
}

// RegistryPublisher defines the configuration for publishing to an OCI registry
type RegistryPublisher struct {
	// RepositoryURL is the URL of the OCI registry repository
	RepositoryURL string `json:"repositoryUrl"`

	// Secret is the name of the secret containing registry credentials
	Secret string `json:"secret"`
}

// ImageBuildStatus defines the observed state of ImageBuild
type ImageBuildStatus struct {
	// Phase represents the current phase of the build (Building, Completed, Failed)
	Phase string `json:"phase,omitempty"`

	// StartTime is when the build started
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the build finished
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Message provides more detail about the current phase
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ImageBuild is the Schema for the imagebuilds API
type ImageBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImageBuildSpec   `json:"spec,omitempty"`
	Status ImageBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageBuildList contains a list of ImageBuild
type ImageBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImageBuild{}, &ImageBuildList{})
}
