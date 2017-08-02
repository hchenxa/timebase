package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/hchenxa/timebase/pkg/client"
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

func main() {
	// Set logging output to standard console out
	log.SetOutput(os.Stdout)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	flag.CommandLine.Parse(make([]string, 0)) // Init for glog calls in kubernetes packages

	apiserverClient, err := client.CreateApiserverClient(*argApiserverHost, *argKubeConfigFile)
	if err != nil {
		handleFatalInitError(err)
	}

	restClient, scheme, err := client.CreateRestClient(*argApiserverHost, *argKubeConfigFile)
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
