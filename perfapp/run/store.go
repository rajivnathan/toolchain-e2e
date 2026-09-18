package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Encode(tr *TestRun) (string, error) {
	b, err := json.Marshal(tr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func Decode(cm *corev1.ConfigMap) (*TestRun, error) {
	raw, ok := cm.Data[StatusKey]
	if !ok || raw == "" {
		return nil, fmt.Errorf("configmap %s/%s is missing status", cm.Namespace, cm.Name)
	}
	tr := &TestRun{}
	if err := json.Unmarshal([]byte(raw), tr); err != nil {
		return nil, fmt.Errorf("decode test run %s: %w", cm.Name, err)
	}
	return tr, nil
}

func Labels(tr *TestRun) map[string]string {
	return map[string]string{
		LabelApp:      AppLabelValue,
		LabelTestRun:  tr.ID,
		LabelTestHost: HostLabel(tr.APIServer),
	}
}

func Create(ctx context.Context, cl client.Client, ns string, tr *TestRun, kubeconfig []byte) error {
	status, err := Encode(tr)
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(tr.ID),
			Namespace: ns,
			Labels:    Labels(tr),
		},
		Data: map[string]string{
			StatusKey: status,
		},
	}
	if err := cl.Create(ctx, cm); err != nil {
		return err
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName(tr.ID),
			Namespace: ns,
			Labels:    Labels(tr),
		},
		Data: map[string][]byte{
			KubeKey: kubeconfig,
		},
	}
	return cl.Create(ctx, sec)
}

func UpdateStatus(ctx context.Context, cl client.Client, ns string, tr *TestRun) error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: ns, Name: ConfigMapName(tr.ID)}
	if err := cl.Get(ctx, key, cm); err != nil {
		return err
	}
	status, err := Encode(tr)
	if err != nil {
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[StatusKey] = status
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	for k, v := range Labels(tr) {
		cm.Labels[k] = v
	}
	return cl.Update(ctx, cm)
}

func Get(ctx context.Context, cl client.Client, ns, id string) (*TestRun, *corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: ConfigMapName(id)}, cm); err != nil {
		return nil, nil, err
	}
	tr, err := Decode(cm)
	if err != nil {
		return nil, cm, err
	}
	return tr, cm, nil
}

func List(ctx context.Context, cl client.Client, ns string) ([]*TestRun, error) {
	list := &corev1.ConfigMapList{}
	if err := cl.List(ctx, list, client.InNamespace(ns), client.MatchingLabels{LabelApp: AppLabelValue}); err != nil {
		return nil, err
	}
	var out []*TestRun
	for i := range list.Items {
		tr, err := Decode(&list.Items[i])
		if err != nil {
			continue
		}
		out = append(out, tr)
	}
	return out, nil
}

func ListNonTerminal(ctx context.Context, cl client.Client, ns string) ([]*TestRun, error) {
	all, err := List(ctx, cl, ns)
	if err != nil {
		return nil, err
	}
	var out []*TestRun
	for _, tr := range all {
		if !tr.Phase.Terminal() {
			out = append(out, tr)
		}
	}
	return out, nil
}

func GetKubeconfig(ctx context.Context, cl client.Client, ns, id string) ([]byte, error) {
	sec := &corev1.Secret{}
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: SecretName(id)}, sec); err != nil {
		return nil, err
	}
	data, ok := sec.Data[KubeKey]
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("secret %s is missing kubeconfig", SecretName(id))
	}
	return data, nil
}

func PutCSV(ctx context.Context, cl client.Client, ns, id string, index int, csv string) error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: ns, Name: ConfigMapName(id)}
	if err := cl.Get(ctx, key, cm); err != nil {
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[ResultsDataKey(index)] = csv
	return cl.Update(ctx, cm)
}

func HasResultsCSV(cm *corev1.ConfigMap) bool {
	if cm == nil {
		return false
	}
	for k := range cm.Data {
		if strings.HasPrefix(k, "results-") && strings.HasSuffix(k, ".csv") {
			return true
		}
	}
	return false
}

func FindOverlap(ctx context.Context, cl client.Client, ns, apiServer string) (string, error) {
	list := &corev1.ConfigMapList{}
	err := cl.List(ctx, list, client.InNamespace(ns), client.MatchingLabels{
		LabelApp:      AppLabelValue,
		LabelTestHost: HostLabel(apiServer),
	})
	if err != nil {
		return "", err
	}
	for i := range list.Items {
		tr, err := Decode(&list.Items[i])
		if err != nil {
			continue
		}
		if !tr.Phase.Terminal() {
			return tr.ID, nil
		}
	}
	return "", nil
}
