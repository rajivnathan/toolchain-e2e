package test

import (
	"context"
	"fmt"

	commontest "github.com/codeready-toolchain/toolchain-common/pkg/test"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func NewScheme(t commontest.T) *runtime.Scheme {
	s := runtime.NewScheme()
	builder := runtime.SchemeBuilder{
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		batchv1.AddToScheme,
		authorizationv1.AddToScheme,
		buildv1.Install,
		imagev1.Install,
	}
	require.NoError(t, builder.AddToScheme(s))
	return s
}

func NewFakeClient(t commontest.T, initObjs ...client.Object) *commontest.FakeClient {
	s := NewScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(initObjs...).
		WithStatusSubresource(&batchv1.Job{}, &buildv1.Build{}, &buildv1.BuildConfig{}).Build()
	return &commontest.FakeClient{Client: cl, T: t}
}

// FakeInstantiator simulates BuildConfig instantiate for tests that have no OpenShift apiserver.
type FakeInstantiator struct {
	Client client.Client
	Err    error
}

func NewFakeInstantiator(cl client.Client) *FakeInstantiator {
	return &FakeInstantiator{Client: cl}
}

func (f *FakeInstantiator) Instantiate(ctx context.Context, namespace, name string, request *buildv1.BuildRequest) (*buildv1.Build, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	bc := &buildv1.BuildConfig{}
	if err := f.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, bc); err != nil {
		return nil, err
	}
	list := &buildv1.BuildList{}
	if err := f.Client.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{buildv1.BuildConfigLabel: name}); err != nil {
		return nil, err
	}
	next := int64(len(list.Items) + 1)
	b := &buildv1.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", name, next),
			Namespace: namespace,
			Labels:    map[string]string{buildv1.BuildConfigLabel: name},
		},
		Spec: buildv1.BuildSpec{CommonSpec: bc.Spec.CommonSpec},
	}
	if request != nil {
		for k, v := range request.Labels {
			b.Labels[k] = v
		}
	}
	if err := f.Client.Create(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func AllowSAR(cl *commontest.FakeClient, allowed bool) {
	cl.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
		if sar, ok := obj.(*authorizationv1.SelfSubjectAccessReview); ok {
			sar.Status.Allowed = allowed
			return nil
		}
		return cl.Client.Create(ctx, obj, opts...)
	}
}
