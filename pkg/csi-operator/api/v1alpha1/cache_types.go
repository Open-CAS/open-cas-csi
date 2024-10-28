/*
Copyright 2024 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CacheSpec defines the desired state of Cache
type CacheSpec struct {
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:Pattern="^\\/dev\\/disk\\/by-id\\/\\S+$"

	// DeviceById defines the block device to be used as cache.
	// Path must be provided as a by-id link path in /dev/disk/by-id dir.
	DeviceById string `json:"deviceById"`

	//+kubebuilder:validation:Optional
	//+kubebuilder:validation:Enum={4,8,16,32,64}

	// CacheLizeSize configures the size of a cache line in kibibytes
	// Allowed values: {4,8,16,32,64}[KiB] (default: 4)
	CacheLizeSize int `json:"cacheLineSize"`

	//+kubebuilder:validation:Optional
	//+kubebuilder:validation:Enum={wt, wa, pt}

	// CacheMode configures the general caching algorithm
	// Allowed values: {wt, wa, pt} (default: wt)
	CacheMode string `json:"cacheMode"`
}

// CacheStatus defines the observed state of Cache
type CacheStatus struct {
	// Indicates that the requested Cache structure is ready.
	Ready bool `json:"ready"`
	// Indicates which Cache Id is to be used in cache ops.
	CacheId int `json:"cacheId,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status

// Cache is the Schema for the caches API
type Cache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required

	Spec   CacheSpec   `json:"spec,omitempty"`
	Status CacheStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CacheList contains a list of Cache
type CacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cache `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cache{}, &CacheList{})
}
