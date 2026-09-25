package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/codeready-toolchain/toolchain-e2e/perfapp/remote"
	"github.com/codeready-toolchain/toolchain-e2e/perfapp/run"

	buildv1 "github.com/openshift/api/build/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Poller struct {
	AppClient     client.Client
	Namespace     string
	NewTestClient func(kubeconfig []byte) (client.Client, error)
}

func (p *Poller) Tick(ctx context.Context) {
	runs, err := run.ListNonTerminal(ctx, p.AppClient, p.Namespace)
	if err != nil {
		log.Printf("poller list: %v", err)
		return
	}
	for _, tr := range runs {
		if err := p.Advance(ctx, tr); err != nil {
			log.Printf("poller %s: %v", tr.ID, err)
		}
	}
}

func (p *Poller) Advance(ctx context.Context, tr *run.TestRun) error {
	_, cm, err := run.Get(ctx, p.AppClient, p.Namespace, tr.ID)
	if err != nil {
		return err
	}
	kube, err := run.GetKubeconfig(ctx, p.AppClient, p.Namespace, tr.ID)
	if err != nil {
		if apierrors.IsNotFound(err) && !run.HasResultsCSV(cm) {
			return p.fail(ctx, tr, "test cluster kubeconfig is gone (cluster torn down mid-run)")
		}
		return err
	}
	testCl, err := p.NewTestClient(kube)
	if err != nil {
		return p.fail(ctx, tr, fmt.Sprintf("cannot create test-cluster client: %v", err))
	}

	switch tr.Phase {
	case run.PhasePrepare:
		return p.advancePrepare(ctx, testCl, tr)
	case run.PhaseRunning:
		return p.advanceStep(ctx, testCl, tr)
	case run.PhaseSucceeded, run.PhaseFailed:
		return nil
	default:
		return nil
	}
}

func (p *Poller) fail(ctx context.Context, tr *run.TestRun, msg string) error {
	tr.Phase = run.PhaseFailed
	tr.LastError = msg
	return run.UpdateStatus(ctx, p.AppClient, p.Namespace, tr)
}

func (p *Poller) advancePrepare(ctx context.Context, testCl client.Client, tr *run.TestRun) error {
	if tr.BuildName == "" {
		return p.fail(ctx, tr, "missing build name")
	}
	b, err := remote.GetBuild(ctx, testCl, tr.BuildName)
	if err != nil {
		return fmt.Errorf("get build: %w", err)
	}
	remote.ApplyBuildStatus(tr, b)
	if remote.BuildFailed(b) {
		msg := string(b.Status.Phase)
		if b.Status.Message != "" {
			msg = b.Status.Message
		}
		if b.Status.LogSnippet != "" {
			msg = msg + "\n" + b.Status.LogSnippet
		}
		return p.fail(ctx, tr, "image build "+msg)
	}
	if b.Status.Phase != buildv1.BuildPhaseComplete {
		return run.UpdateStatus(ctx, p.AppClient, p.Namespace, tr)
	}
	if len(tr.Steps) == 0 {
		return p.fail(ctx, tr, "test run has no steps")
	}
	tr.Phase = run.PhaseRunning
	tr.StepIndex = 0
	return p.ensureStep(ctx, testCl, tr)
}

func (p *Poller) advanceStep(ctx context.Context, testCl client.Client, tr *run.TestRun) error {
	if tr.StepIndex < 0 || tr.StepIndex >= len(tr.Steps) {
		return p.fail(ctx, tr, fmt.Sprintf("step index %d out of range", tr.StepIndex))
	}
	name := tr.Steps[tr.StepIndex].Job
	if name == "" {
		name = run.StepJobName(tr.StepIndex, tr.ID)
	}
	job, err := remote.GetJob(ctx, testCl, name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get job %s: %w", name, err)
	}
	if !remote.JobTerminal(job) {
		return nil
	}
	if remote.JobFailed(job) {
		p.markCurrentFailed(tr, remote.JobMessage(job))
		return run.UpdateStatus(ctx, p.AppClient, p.Namespace, tr)
	}
	if tr.Steps[tr.StepIndex].Kind == run.StepSetup {
		if err := remote.CopyResults(ctx, testCl, p.AppClient, p.Namespace, tr, tr.StepIndex); err != nil {
			return p.fail(ctx, tr, err.Error())
		}
	}
	tr.Steps[tr.StepIndex].Status = run.StepSucceeded
	next := tr.StepIndex + 1
	if next >= len(tr.Steps) {
		tr.Phase = run.PhaseSucceeded
		return run.UpdateStatus(ctx, p.AppClient, p.Namespace, tr)
	}
	tr.StepIndex = next
	return p.ensureStep(ctx, testCl, tr)
}

func (p *Poller) markCurrentFailed(tr *run.TestRun, msg string) {
	tr.Phase = run.PhaseFailed
	tr.LastError = msg
	if tr.StepIndex >= 0 && tr.StepIndex < len(tr.Steps) {
		tr.Steps[tr.StepIndex].Status = run.StepFailed
	}
}

func (p *Poller) ensureStep(ctx context.Context, testCl client.Client, tr *run.TestRun) error {
	job, err := remote.StepJob(tr, tr.StepIndex)
	if err != nil {
		return p.fail(ctx, tr, err.Error())
	}
	got, _, err := remote.EnsureJob(ctx, testCl, job)
	if err != nil {
		return p.fail(ctx, tr, fmt.Sprintf("create step %d Job: %v", tr.StepIndex, err))
	}
	tr.Steps[tr.StepIndex].Job = got.Name
	tr.Steps[tr.StepIndex].Status = run.StepRunning
	return run.UpdateStatus(ctx, p.AppClient, p.Namespace, tr)
}

func (p *Poller) Run(ctx context.Context, interval time.Duration) {
	p.Tick(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Tick(ctx)
		}
	}
}
