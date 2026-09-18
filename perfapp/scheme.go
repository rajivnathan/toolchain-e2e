package main

import (
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Scheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	builder := runtime.SchemeBuilder{
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		batchv1.AddToScheme,
		authorizationv1.AddToScheme,
		buildv1.Install,
		imagev1.Install,
	}
	return s, builder.AddToScheme(s)
}
