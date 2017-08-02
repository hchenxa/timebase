package client

import (
	"log"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	policyapi "github.com/hchenxa/timebase/pkg/api/icp.ibm.com/v1"
)

func buildConfigFromFlags(masterURL, kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath == "" && masterURL == "" {
		kubeconfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		return kubeconfig, nil
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterURL}}).ClientConfig()
}

// CreateApiserverClient create a clientset
func CreateApiserverClient(apiserverHost, kubeConfig string) (*kubernetes.Clientset, error) {
	cfg, err := buildConfigFromFlags(apiserverHost, kubeConfig)
	if err != nil {
		return nil, err
	}

	cfg.ContentType = "application/json"

	log.Printf("Creating API server client for %s", cfg.Host)

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// CreateRestClient creates a rest client
func CreateRestClient(apiserverHost, kubeConfig string) (*rest.RESTClient, *runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := policyapi.AddToScheme(scheme); err != nil {
		return nil, err
	}

	cfg, err := buildConfigFromFlags(apiserverHost, kubeConfig)
	if err != nil {
		return nil, nil, err
	}

	config := *cfg

	config.GroupVersion = &policyapi.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, nil, err
	}

	return client, scheme, nil
}
