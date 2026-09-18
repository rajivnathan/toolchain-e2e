package results

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"

	cfg "github.com/codeready-toolchain/toolchain-e2e/setup/configuration"
	"github.com/codeready-toolchain/toolchain-e2e/setup/terminal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ResultsCSVKey = "results.csv"

type Writer interface {
	Write([][]string) error
	Close() error
}

type Results struct {
	stdOutWriter  Writer
	csvWriter     Writer
	results       [][]string
	term          terminal.Terminal
	cl            client.Client
	configMapName string
	namespace     string
}

func New(term terminal.Terminal, cl client.Client, configMapName, namespace string) *Results {
	csvFile, err := os.Create(cfg.ResultsFilepath())
	if err != nil {
		term.Infof("failed creating file: %s", err)
		os.Exit(1)
	}

	return &Results{
		results:       make([][]string, 0),
		csvWriter:     csvWriter{csvFile},
		stdOutWriter:  terminalWriter{term},
		term:          term,
		cl:            cl,
		configMapName: configMapName,
		namespace:     namespace,
	}
}

func (r *Results) writeResults() error {
	for _, w := range []Writer{r.stdOutWriter, r.csvWriter} {
		if err := w.Write(r.results); err != nil {
			return err
		}
	}
	return nil
}

func (r *Results) AddResults(results [][]string) {
	r.results = append(r.results, results...)
}

type csvWriter struct {
	f *os.File
}

func (w csvWriter) Write(results [][]string) error {
	writer := csv.NewWriter(w.f)
	return writer.WriteAll(results)
}

func (w csvWriter) Close() error {
	return w.f.Close()
}

type terminalWriter struct {
	t terminal.Terminal
}

func (w terminalWriter) Write(results [][]string) error {
	for _, result := range results {
		w.t.Infof("%s: %s", result[0], result[1])
	}
	return nil
}

func (w terminalWriter) Close() error {
	return nil
}

// OutputResults outputs the aggregated results to the terminal and a csv file.
// When a ConfigMap name is set, the same CSV bytes are also written to data key results.csv.
func (r *Results) OutputResults() {
	if err := r.writeResults(); err != nil {
		r.term.Fatalf(err, "failed to write results")
	}
	if err := r.csvWriter.Close(); err != nil {
		r.term.Fatalf(err, "failed to close results file")
	}

	r.term.Info("\nResults file: " + cfg.ResultsFilepath())

	if err := r.writeConfigMap(); err != nil {
		r.term.Fatalf(err, "failed to write results ConfigMap")
	}
}

func (r *Results) writeConfigMap() error {
	if r.configMapName == "" {
		return nil
	}
	if r.cl == nil {
		return fmt.Errorf("results ConfigMap %q requested but no Kubernetes client was provided", r.configMapName)
	}
	if r.namespace == "" {
		return fmt.Errorf("results ConfigMap %q requested but namespace is empty", r.configMapName)
	}

	csvBytes, err := os.ReadFile(cfg.ResultsFilepath()) //nolint:gosec
	if err != nil {
		return err
	}

	ctx := context.TODO()
	key := types.NamespacedName{Namespace: r.namespace, Name: r.configMapName}
	cm := &corev1.ConfigMap{}
	err = r.cl.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.configMapName,
				Namespace: r.namespace,
			},
			Data: map[string]string{
				ResultsCSVKey: string(csvBytes),
			},
		}
		return r.cl.Create(ctx, cm)
	}
	if err != nil {
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[ResultsCSVKey] = string(csvBytes)
	return r.cl.Update(ctx, cm)
}
