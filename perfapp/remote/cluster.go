package remote

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func EnsureNamespace(ctx context.Context, cl client.Client) error {
	ns := &corev1.Namespace{}
	err := cl.Get(ctx, types.NamespacedName{Name: run.TestNamespace}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: run.TestNamespace}}
	return cl.Create(ctx, ns)
}

func EnsureRunner(ctx context.Context, cl client.Client) error {
	sa := &corev1.ServiceAccount{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: run.RunnerSA}, sa)
	if apierrors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      run.RunnerSA,
			Namespace: run.TestNamespace,
		}}
		if err := cl.Create(ctx, sa); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	crb := &rbacv1.ClusterRoleBinding{}
	err = cl.Get(ctx, types.NamespacedName{Name: run.RunnerCRB}, crb)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	crb = &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: run.RunnerCRB},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      run.RunnerSA,
			Namespace: run.TestNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}
	return cl.Create(ctx, crb)
}

func WriteTemplates(ctx context.Context, cl client.Client, name string, content []byte) error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: run.TestNamespace, Name: run.TemplatesCMName}
	err := cl.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      run.TemplatesCMName,
				Namespace: run.TestNamespace,
			},
			Data: map[string]string{name: string(content)},
		}
		return cl.Create(ctx, cm)
	}
	if err != nil {
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[name] = string(content)
	return cl.Update(ctx, cm)
}

func CopyResults(ctx context.Context, testCl, appCl client.Client, appNS string, tr *run.TestRun, index int) error {
	src := &corev1.ConfigMap{}
	err := testCl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: run.ResultsCMName(index)}, src)
	if err != nil {
		return fmt.Errorf("results ConfigMap %s: %w", run.ResultsCMName(index), err)
	}
	csv, ok := src.Data[run.ResultsCSVKey]
	if !ok || csv == "" {
		return fmt.Errorf("results ConfigMap %s is missing %s", run.ResultsCMName(index), run.ResultsCSVKey)
	}
	return run.PutCSV(ctx, appCl, appNS, tr.ID, index, csv)
}
