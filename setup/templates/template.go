package templates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	applyclientlib "github.com/codeready-toolchain/toolchain-common/pkg/client"

	multierror "github.com/hashicorp/go-multierror"
	templatev1 "github.com/openshift/api/template/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubectl/pkg/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const fieldManager = "e2e-tests"

// applyRetryInterval is the pause between apply attempts while a CRD is still
// being registered. The first attempt runs immediately.
var applyRetryInterval = 5 * time.Second

// applyTimeout bounds retries when the target kind is not registered yet.
// Component CRDs such as OdhDashboardConfig are created only after the operator
// reconciles DataScienceCluster, which can take several minutes after the
// operator CSV reaches Succeeded.
var applyTimeout = 10 * time.Minute

func GetTemplateFromFile(filepath string) (*templatev1.Template, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	return GetTemplateFromContent(content)
}

func GetTemplateFromContent(content []byte) (*templatev1.Template, error) {
	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	tmpl := &templatev1.Template{}
	_, gvk, err := decoder.Decode(content, nil, tmpl)
	if err != nil {
		return nil, err
	}
	if gvk.Kind == "Template" { // expect an OpenShift template
		return tmpl, nil
	}
	return nil, fmt.Errorf("wrong kind of object in the template file: '%s'", gvk)
}

// ApplyObjects applies the given objects in order
func ApplyObjects(ctx context.Context, cl runtimeclient.Client, objsToApply []runtimeclient.Object, modifiers ...ClientObjectModifier) error {
	applycl := applyclientlib.NewServerSideApplyClient(cl, fieldManager)
	for _, obj := range objsToApply {
		fmt.Printf("Applying %s object with name '%s' in namespace '%s'\n", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), obj.GetNamespace())
		if err := applyObject(ctx, applycl, obj, modifiers...); err != nil {
			return err
		}
	}
	return nil
}

// ApplyObjectsConcurrently applies multiple objects concurrently
func ApplyObjectsConcurrently(ctx context.Context, cl runtimeclient.Client, combinedObjsToProcess []runtimeclient.Object, modifiers ...ClientObjectModifier) error {
	var objProcessors []<-chan error
	objChannel := distribute(combinedObjsToProcess)
	for i := 0; i < len(combinedObjsToProcess); i++ {
		objProcessors = append(objProcessors, startObjectProcessor(ctx, cl, objChannel, modifiers...))
		time.Sleep(100 * time.Millisecond) // wait for a short time before starting each object processor to avoid hitting rate limits
	}

	// combine the results
	var overallErr error
	for err := range combineResults(objProcessors...) {
		if err != nil {
			overallErr = multierror.Append(overallErr, err)
		}
	}

	return overallErr
}

func distribute(objs []runtimeclient.Object) <-chan runtimeclient.Object {
	out := make(chan runtimeclient.Object)
	go func() {
		for _, obj := range objs {
			out <- obj
		}
		close(out)
	}()
	return out
}

func combineResults(results ...<-chan error) <-chan error {
	var wg sync.WaitGroup
	out := make(chan error)

	// Start an output goroutine for each input channel in results.
	// output copies values from results to out until results is closed, then calls wg.Done.
	output := func(c <-chan error) {
		for r := range c {
			out <- r
		}
		wg.Done()
	}
	wg.Add(len(results))
	for _, result := range results {
		go output(result)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func startObjectProcessor(ctx context.Context, cl runtimeclient.Client, objSource <-chan runtimeclient.Object, modifiers ...ClientObjectModifier) <-chan error {
	out := make(chan error)
	go func() {
		applycl := applyclientlib.NewServerSideApplyClient(cl, fieldManager)
		for obj := range objSource {
			out <- applyObject(ctx, applycl, obj, modifiers...)
			time.Sleep(100 * time.Millisecond)
		}
		close(out)
	}()
	return out
}

type ClientObjectModifier func(obj runtimeclient.Object) error

func NamespaceModifier(userNS string) ClientObjectModifier {
	return func(obj runtimeclient.Object) error {
		// enforce the creation of the objects in the `userNS` namespace
		obj.SetNamespace(userNS)
		return nil
	}
}

func applyObject(ctx context.Context, applycl *applyclientlib.ServerSideApplyClient, obj runtimeclient.Object, modifiers ...ClientObjectModifier) error {
	// apply any modifiers before applying the object
	for _, modifier := range modifiers {
		if err := modifier(obj); err != nil {
			return err
		}
	}

	// Returning an error from the poll stops it immediately, so only keep going
	// for "no matches for kind". That happens when a post-install CR is applied
	// before the operator has created its CRD.
	var lastErr error
	err := k8swait.PollUntilContextTimeout(ctx, applyRetryInterval, applyTimeout, true, func(context.Context) (bool, error) {
		applyErr := applycl.ApplyObject(ctx, obj)
		if applyErr == nil {
			return true, nil
		}
		if meta.IsNoMatchError(applyErr) {
			lastErr = applyErr
			fmt.Printf("kind %s is not registered yet; retrying apply of '%s'\n", obj.GetObjectKind().GroupVersionKind(), obj.GetName())
			return false, nil
		}
		return false, applyErr
	})
	if err != nil {
		if lastErr != nil && errors.Is(err, context.DeadlineExceeded) {
			err = lastErr
		}
		return fmt.Errorf("could not apply resource '%s' in namespace '%s': %w", obj.GetName(), obj.GetNamespace(), err)
	}
	return nil
}
