/*
Copyright 2024 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

// +kubebuilder:object:generate=true
// +groupName=csi.open-cas.com

// Package v1alpha1 contains API Schema definitions for the cache v1alpha1 API group
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "csi.open-cas.com", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
