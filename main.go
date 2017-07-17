package main

import (
	"fmt"
	"time"
	"log"
	"os"
	"flag"

	"github.com/golang/glog"
	"github.com/robfig/cron"
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	extensionsclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/extensions/v1beta1"
	autoscalingclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/autoscaling/v1"
	"k8s.io/client-go/rest"
	"github.com/spf13/pflag"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

)

const (
	// A path to the endpoint for the CronTab custom resources.
	policyEndpoint = "http://%s/apis/alpha.ianlewis.org/v1/namespaces/%s/policies"
)


// lables correspond to labels in the Kubernetes API.
type labels *map[string]string

// objectMeta corresponds to object metadata in the Kubernetes API.
type objectMeta struct {
	Name            string `json:"name"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Labels          labels `json:"labels,omitempty"`
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,3,opt,name=namespace"`
}
//
// type policyList struct {
// 	Items []PolicyTab `json:"items"`
// }

// cronTab represents a JSON object for the CronTab custom resource that we register in the Kubernetes API.
type PolicyTab struct {
	// The following fields mirror the fields in the third party resource.
	metav1.TypeMeta `json:",inline"`
	ObjectMeta objectMeta `json:"metadata,omitempty"`
	Status     Status    `json:"status"`
	Spec       policySpec `json:"spec"`
}

type Status struct {
	CreationTimestamp *metav1.Time
	LastScheduleTime *metav1.Time
}

type policySpec struct {
	Action      ActionSpec      `json:"action"`
	Schedule    string          `json:"schedule"`
	ScaleTargetRef   CrossVersionObjectReference  `json:"scaleTargetRef" protobuf:"bytes,1,opt,name=scaleTargetRef"`
	TargetReplicas int32 `json:"replicas,omitempty" protobuf:"varint,2,opt,name=replicas"`
}

type CrossVersionObjectReference struct {
	// Kind of the referent; More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#types-kinds"
	Kind string `json:"kind" protobuf:"bytes,1,opt,name=kind"`
	// Name of the referent; More info: http://kubernetes.io/docs/user-guide/identifiers#names
	Name string `json:"name" protobuf:"bytes,2,opt,name=name"`
	// API version of the referent
	// +optional
	APIVersion string `json:"apiVersion,omitempty" protobuf:"bytes,3,opt,name=apiVersion"`
}

type ActionSpec string

const (
	ScaleUp ActionSpec = "scaleUp"
	ScaleDown ActionSpec = "scaleDown"
)

type PolicyLister struct {
	cache.Store
}

type TimebasedController struct {
	cfg *Configuration

	scaleNamespacer extensionsclient.ScalesGetter
	hpaNamespacer   autoscalingclient.HorizontalPodAutoscalersGetter

  policyController cache.Controller
  policyLister     PolicyLister

  stopCh chan struct{}
}

type Configuration struct {
	Client       clientset.Interface
	ResyncPeriod time.Duration
}

func NewTimebasedController(config *Configuration) *TimebasedController {
	policy := TimebasedController{
		cfg:    config,
		stopCh: make(chan struct{}),
	}

	policy.policyLister.Store, policy.policyController = cache.NewInformer(
		cache.NewListWatchFromClient(policy.cfg.Client.Apps().RESTClient(), "policies", v1.NamespaceAll, fields.Everything()),
		&PolicyTab{}, policy.cfg.ResyncPeriod, cache.ResourceEventHandlerFuncs{})

	return &policy
}

func (a *TimebasedController) Run(stopCh <-chan struct{}) {
	// start a single worker (we may wish to start more in the future)
	go wait.Until(a.worker, time.Second, stopCh)

	<-stopCh
}

func (a *TimebasedController) worker() {

	pl := a.policyLister.Store.List()

	// policies := pl.Items
  // glog.Infof("the policy list was: %s", pl.string)
	for _, pIf := range pl {
		p := pIf.(*PolicyTab)
		return a.reconcileAutoscaler(p, time.Now())
	}

}

func getRecentUnmetScheduleTimes(p PolicyTab, now time.Time) ([]time.Time, error) {
	starts := []time.Time{}
	sched, err := cron.ParseStandard(p.Spec.Schedule)
	if err != nil {
		return starts, fmt.Errorf("Unparseable schedule: %s : %s", p.Spec.Schedule, err)
	}

	var earliestTime time.Time
	if p.Status.LastScheduleTime != nil {
		earliestTime = p.Status.LastScheduleTime.Time
	} else {
		earliestTime = p.Status.CreationTimestamp.Time
	}
	if earliestTime.After(now) {
		return []time.Time{}, nil
	}

	for t := sched.Next(earliestTime); !t.After(now); t = sched.Next(t) {
		starts = append(starts, t)

		if len(starts) > 100 {
			// We can't get the most recent times so just return an empty slice
			return []time.Time{}, fmt.Errorf("Too many missed start time (> 100). Set or decrease .spec.startingDeadlineSeconds or check clock skew.")
		}
	}
	return starts, nil
}

func (a *TimebasedController) reconcileAutoscaler(p *PolicyTab, now time.Time) {

	reference := fmt.Sprintf("%s/%s/%s", p.Spec.ScaleTargetRef.Kind, p.ObjectMeta.Namespace, p.Spec.ScaleTargetRef.Name)

	scale, err := a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Get(p.Spec.ScaleTargetRef.Kind, p.Spec.ScaleTargetRef.Name)
	if err != nil {
		return fmt.Errorf("failed to query scale subresource for %s: %v", reference, err)
	}

	times, err := getRecentUnmetScheduleTimes(*p, now)
	if err != nil {
		glog.Errorf("Cannot determine needs to be started: %v", err)
	}
	// TODO: handle multiple unmet start times, from oldest to newest, updating status as needed.
	if len(times) == 0 {
		glog.V(4).Infof("No unmet start times")
		return
	}
	if len(times) > 1 {
		glog.V(4).Infof("Multiple unmet start times so only starting last one")
	}

	currentReplicas := scale.Status.Replicas

	if p.Spec.Action == ScaleUp {
		if p.Spec.TargetReplicas <= currentReplicas {
			glog.V(4).Infof("The request replicas was less than current replicas, no need to scale up")
			return
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
			return
		}
	} else {
		if p.Spec.TargetReplicas >= currentReplicas {
			glog.V(4).Infof("the request replicas was large than replicas, no need to scale down")
			return
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
			return
		}
	}
}

func buildConfigFromFlags() (*rest.Config, error) {
	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubeconfig, nil
}

func CreateApiserverClient() (*kubernetes.Clientset, *rest.Config, error) {
	cfg, err := buildConfigFromFlags()
	if err != nil {
		return nil, nil, err
	}

	cfg.ContentType = "application/vnd.kubernetes.protobuf"

	log.Printf("Creating API server client for %s", cfg.Host)

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	return client, cfg, nil
}

func main() {
    // Set logging output to standard console out
    log.SetOutput(os.Stdout)

    pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
    pflag.Parse()
    flag.CommandLine.Parse(make([]string, 0)) // Init for glog calls in kubernetes packages

    apiserverClient, config, err := CreateApiserverClient()
    if err != nil {
        handleFatalInitError(err)
    }

    versionInfo, err := apiserverClient.ServerVersion()
    if err != nil {
        handleFatalInitError(err)
    }
    log.Printf("Successful initial request to the apiserver, version: %s", versionInfo.String())

    pc := NewTimebasedController(&Configuration{
        Client:       apiserverClient,
        ResyncPeriod: 5 * time.Minute,
    })

    pc.Run()
}

func handleFatalInitError(err error) {
	log.Fatalf("Error while initializing connection to Kubernetes apiserver. "+
		"This most likely means that the cluster is misconfigured (e.g., it has "+
		"invalid apiserver certificates or service accounts configuration) or the "+
		"--apiserver-host param points to a server that does not exist. Reason: %s\n"+
		"Refer to the troubleshooting guide for more information: "+
		"https://github.com/kubernetes/dashboard/blob/master/docs/user-guide/troubleshooting.md", err)
}
