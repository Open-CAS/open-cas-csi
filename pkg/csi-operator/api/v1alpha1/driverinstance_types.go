/*
Copyright 2024 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DriverInstanceSpec defines the desired state of DriverInstance
type DriverInstanceSpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// Specifies how verbose logs should be.
	LogLevel uint16 `json:"logLevel,omitempty"`

	//+kubebuilder:Validation:Required

	// Specifies node labels that will determine on which nodes
	// the driver should provision volumes.
	NodeSelector map[string]string `json:"nodeSelector"`

	// Specifies additional labels for objects created by the operator.
	Labels map[string]string `json:"labels,omitempty"`

	// Specifies arguments required for building images (e.g. proxy)
	BuildArgs map[string]string `json:"buildArgs,omitempty"`

	//+kubebuilder:Validation:Required

	// Contains configuration for deployment of Open-CAS CSI Driver
	DriverConfig DriverConf `json:"driverConfig"`
}

// DriverInstanceStatus defines the observed state of DriverInstance
type DriverInstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// DriverConf defines the configuration specific for deploying Open-CAS CSI Driver
type DriverConf struct {
	//+kubebuilder:Validation:Required

	// Specifies location of Open-CAS CSI Driver image
	DriverImage string `json:"driverImage"`

	//+kubebuilder:Validation:Required

	// Specifies location of management (casadm) image
	CasadmImage string `json:"casadmImage,omitempty"`

	// Specifies location of webhook server image
	// If not provided intermediate pods will not be protected
	WebhookImage string `json:"webhookImage,omitempty"`

	//+kubebuilder:Validation:Required

	// Specifies Open-CAS CSI Driver instance namespace
	DriverNamespace string `json:"driverNamespace"`

	// Specifies namespace for intermediate pods (protected from deletion if webhook image provided)
	ProtectedNamespace string `json:"protectedNamespace,omitempty"`

	//                                                      TODO (add resources constraints)

	// +kubebuilder:validation:Minimum=0

	// Specifies how many copies of controller pod are to be deployed in the cluster (default=1).
	ControllerReplicas int32 `json:"controllerReplicas,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status

// DriverInstance is the Schema for the driverinstances API
// Namespace/Name determines Open-CAS CSI driver Namespace/Name
type DriverInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DriverInstanceSpec   `json:"spec,omitempty"`
	Status DriverInstanceStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DriverInstanceList contains a list of DriverInstance
type DriverInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DriverInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DriverInstance{}, &DriverInstanceList{})
}

func (di *DriverInstance) GetDefaultLabels() map[string]string {
	labels := make(map[string]string)
	labels["app.kubernetes.io/name"] = di.Name
	labels["app.kubernetes.io/part-of"] = "opencas-csi-driver"
	labels["app.kubernetes.io/managed-by"] = "opencas-csi-operator"
	return labels
}
