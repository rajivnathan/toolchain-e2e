package remote

import (
	"context"
	"fmt"

	buildv1 "github.com/openshift/api/build/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

// Instantiator starts a Build from a BuildConfig via the instantiate subresource
// (the same API oc start-build uses).
type Instantiator interface {
	Instantiate(ctx context.Context, namespace, name string, request *buildv1.BuildRequest) (*buildv1.Build, error)
}

type restInstantiator struct {
	client rest.Interface
}

// NewInstantiator returns an Instantiator for the OpenShift build.openshift.io API.
func NewInstantiator(cfg *rest.Config) (Instantiator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("rest config is required to instantiate builds")
	}
	rc, err := newBuildRESTClient(cfg)
	if err != nil {
		return nil, err
	}
	return &restInstantiator{client: rc}, nil
}

func newBuildRESTClient(cfg *rest.Config) (rest.Interface, error) {
	s := runtime.NewScheme()
	if err := buildv1.Install(s); err != nil {
		return nil, err
	}
	metav1.AddToGroupVersion(s, schema.GroupVersion{Version: "v1"})

	restCfg := rest.CopyConfig(cfg)
	gv := buildv1.SchemeGroupVersion
	restCfg.GroupVersion = &gv
	restCfg.APIPath = "/apis"
	restCfg.ContentType = runtime.ContentTypeJSON
	restCfg.NegotiatedSerializer = serializer.NewCodecFactory(s).WithoutConversion()
	if restCfg.UserAgent == "" {
		restCfg.UserAgent = rest.DefaultKubernetesUserAgent()
	}
	httpClient, err := rest.HTTPClientFor(restCfg)
	if err != nil {
		return nil, err
	}
	return rest.RESTClientForConfigAndClient(restCfg, httpClient)
}

func (i *restInstantiator) Instantiate(ctx context.Context, namespace, name string, request *buildv1.BuildRequest) (*buildv1.Build, error) {
	result := &buildv1.Build{}
	err := i.client.Post().
		Namespace(namespace).
		Resource("buildconfigs").
		Name(name).
		SubResource("instantiate").
		Body(request).
		Do(ctx).
		Into(result)
	return result, err
}
