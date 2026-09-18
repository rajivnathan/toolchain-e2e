package remote

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// imageStreamReadyTimeout is how long Prepare waits for the ImageStream status
// dockerImageRepository (the integrated registry hostname). Fake clients never
// populate that field; tests set this to 0.
var imageStreamReadyTimeout = 30 * time.Second

// SetImageStreamReadyTimeout is for tests that do not populate ImageStream status.
func SetImageStreamReadyTimeout(d time.Duration) {
	imageStreamReadyTimeout = d
}

func gitSourceSpec(imageTag string) buildv1.CommonSpec {
	return buildv1.CommonSpec{
		Source: buildv1.BuildSource{
			Type: buildv1.BuildSourceGit,
			Git: &buildv1.GitBuildSource{
				URI: run.GitURI,
				Ref: run.GitRef,
			},
		},
		Strategy: buildv1.BuildStrategy{
			Type: buildv1.DockerBuildStrategyType,
			DockerStrategy: &buildv1.DockerBuildStrategy{
				DockerfilePath: run.DockerfilePath,
			},
		},
		Output: buildv1.BuildOutput{
			To: &corev1.ObjectReference{
				Kind: "ImageStreamTag",
				Name: run.ImageStreamName + ":" + imageTag,
			},
		},
	}
}

func EnsureImageStream(ctx context.Context, cl client.Client) error {
	is := &imagev1.ImageStream{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: run.ImageStreamName}, is)
	if apierrors.IsNotFound(err) {
		is = &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{
			Name:      run.ImageStreamName,
			Namespace: run.TestNamespace,
		}}
		if err := cl.Create(ctx, is); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return waitForImageStreamRepository(ctx, cl)
}

func waitForImageStreamRepository(ctx context.Context, cl client.Client) error {
	if imageStreamReadyTimeout <= 0 {
		return nil
	}
	key := types.NamespacedName{Namespace: run.TestNamespace, Name: run.ImageStreamName}
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, imageStreamReadyTimeout, true, func(ctx context.Context) (bool, error) {
		is := &imagev1.ImageStream{}
		if err := cl.Get(ctx, key, is); err != nil {
			return false, err
		}
		return is.Status.DockerImageRepository != "", nil
	})
	if err != nil {
		return fmt.Errorf("imagestream %s has no dockerImageRepository (integrated registry not ready): %w", run.ImageStreamName, err)
	}
	return nil
}

func EnsureBuildConfig(ctx context.Context, cl client.Client, imageTag string) error {
	bc := &buildv1.BuildConfig{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: run.BuildConfigName}, bc)
	spec := gitSourceSpec(imageTag)
	if apierrors.IsNotFound(err) {
		bc = &buildv1.BuildConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      run.BuildConfigName,
				Namespace: run.TestNamespace,
			},
			Spec: buildv1.BuildConfigSpec{CommonSpec: spec},
		}
		return cl.Create(ctx, bc)
	}
	if err != nil {
		return err
	}
	bc.Spec.CommonSpec = spec
	return cl.Update(ctx, bc)
}

func StartBuild(ctx context.Context, inst Instantiator, tr *run.TestRun) (*buildv1.Build, error) {
	if inst == nil {
		return nil, fmt.Errorf("start-build: instantiator is required")
	}
	req := &buildv1.BuildRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: buildv1.GroupVersion.String(),
			Kind:       "BuildRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: run.BuildConfigName,
			Labels: map[string]string{
				run.LabelTestRun: tr.ID,
			},
		},
		TriggeredBy: []buildv1.BuildTriggerCause{{
			Message: fmt.Sprintf("onboarding perfapp test run %s", tr.ID),
		}},
	}
	b, err := inst.Instantiate(ctx, run.TestNamespace, run.BuildConfigName, req)
	if err != nil {
		return nil, fmt.Errorf("start-build: instantiate: %w", err)
	}
	return b, nil
}

func GetBuild(ctx context.Context, cl client.Client, name string) (*buildv1.Build, error) {
	b := &buildv1.Build{}
	err := cl.Get(ctx, types.NamespacedName{Namespace: run.TestNamespace, Name: name}, b)
	return b, err
}

func BuildFailed(b *buildv1.Build) bool {
	return b.Status.Phase == buildv1.BuildPhaseFailed ||
		b.Status.Phase == buildv1.BuildPhaseError ||
		b.Status.Phase == buildv1.BuildPhaseCancelled
}

func Prepare(ctx context.Context, cl client.Client, inst Instantiator, tr *run.TestRun, template []byte, templateName string) error {
	if err := EnsureNamespace(ctx, cl); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	if err := EnsureRunner(ctx, cl); err != nil {
		return fmt.Errorf("ensure runner SA: %w", err)
	}
	if err := RejectActiveJobs(ctx, cl); err != nil {
		return err
	}
	if len(template) > 0 {
		if templateName == "" {
			templateName = "template.yaml"
		}
		if err := WriteTemplates(ctx, cl, templateName, template); err != nil {
			return fmt.Errorf("write templates: %w", err)
		}
	}
	if tr.ImageTag == "" {
		tr.ImageTag = tr.ID
	}
	if err := EnsureImageStream(ctx, cl); err != nil {
		return fmt.Errorf("ensure imagestream: %w", err)
	}
	if err := EnsureBuildConfig(ctx, cl, tr.ImageTag); err != nil {
		return fmt.Errorf("ensure buildconfig: %w", err)
	}
	b, err := StartBuild(ctx, inst, tr)
	if err != nil {
		return err
	}
	tr.BuildName = b.Name
	return nil
}
