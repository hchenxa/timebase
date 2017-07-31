package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	policyapi "github.com/hchenxa/timebase/pkg/api"
	"github.com/hchenxa/timebase/pkg/controller"
)

var (
	argApiserverHost = pflag.String("apiserver-host", "", "The address of the Kubernetes Apiserver "+
		"to connect to in the format of protocol://address:port, e.g., "+
		"http://localhost:8080. If not specified, the assumption is that the binary runs inside a "+
		"Kubernetes cluster and local discovery is attempted.")
	argKubeConfigFile = pflag.String("kubeconfig", "", "Path to kubeconfig file with authorization and master location information.")
)

// lables correspond to labels in the Kubernetes API.
type labels *map[string]string

func buildConfigFromFlags(masterUrl, kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath == "" && masterUrl == "" {
		kubeconfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		return kubeconfig, nil
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterUrl}}).ClientConfig()
}

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

func CreateRestClient(apiserverHost, kubeConfig string) (*rest.RESTClient, error) {
	cfg, err := buildConfigFromFlags(apiserverHost, kubeConfig)
	if err != nil {
		return nil, err
	}

	cfg.GroupVersion = &schema.GroupVersion{
		Group:   policyapi.APIGroup,
		Version: policyapi.APIVersion,
	}
	cfg.APIPath = "/apis"
	cfg.ContentType = runtime.ContentTypeJSON
	cfg.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: scheme.Codecs}

	schemeBuilder := runtime.NewSchemeBuilder(policyapi.AddKnownTypes)
	schemeBuilder.AddToScheme(runtime.NewScheme())

	return rest.RESTClientFor(cfg)
}

func main() {
	// Set logging output to standard console out
	log.SetOutput(os.Stdout)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	flag.CommandLine.Parse(make([]string, 0)) // Init for glog calls in kubernetes packages

	apiserverClient, err := CreateApiserverClient(*argApiserverHost, *argKubeConfigFile)
	if err != nil {
		handleFatalInitError(err)
	}

	restClient, err := CreateRestClient(*argApiserverHost, *argKubeConfigFile)
	if err != nil {
		handleFatalInitError(err)
	}

	versionInfo, err := apiserverClient.ServerVersion()
	if err != nil {
		handleFatalInitError(err)
	}
	log.Printf("Successful initial request to the apiserver, version: %s", versionInfo.String())

	pc := controller.NewTimebasedController(&controller.Configuration{
		Client:       restClient,
		ResyncPeriod: 5 * time.Minute,
	})

	stop := make(chan struct{})

	pc.Run(stop)
}

func handleFatalInitError(err error) {
	log.Fatalf("Error while initializing connection to Kubernetes apiserver. "+
		"This most likely means that the cluster is misconfigured (e.g., it has "+
		"invalid apiserver certificates or service accounts configuration) or the "+
		"--apiserver-host param points to a server that does not exist. Reason: %s\n"+
		"Refer to the troubleshooting guide for more information: "+
		"https://github.com/kubernetes/dashboard/blob/master/docs/user-guide/troubleshooting.md", err)
}
