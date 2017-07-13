package main

import (
	"fmt"
	"math"
	"time"

	"github.com/golang/glog"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2alpha1"
	"k8s.io/api/core/v1"
	clientv1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/api"
	autoscalingclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/autoscaling/v1"
	extensionsclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/extensions/v1beta1"
	autoscalinginformers "k8s.io/kubernetes/pkg/client/informers/informers_generated/externalversions/autoscaling/v1"
	autoscalinglisters "k8s.io/kubernetes/pkg/client/listers/autoscaling/v1"
	"k8s.io/kubernetes/pkg/controller"
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
}

type policyList struct {
	Items []policy `json:"items"`
}

// cronTab represents a JSON object for the CronTab custom resource that we register in the Kubernetes API.
type policy struct {
	// The following fields mirror the fields in the third party resource.
	ObjectMeta objectMeta  `json:"metadata"`
	Spec       policySpec `json:"spec"`
}

type policySpec struct {
	Action      actionSpec      `json:"action"`
	Schedule    string          `json:"schedule"`
	JobTemplate jobTemplateSpec `json:"jobTemplate"`
	ScaleTargetRef   CrossVersionObjectReference  `json:"scaleTargetRef" protobuf:"bytes,1,opt,name=scaleTargetRef"`
	TargetReplicas *int32 `json:"replicas,omitempty" protobuf:"varint,2,opt,name=replicas"`
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

type actionSpec struct {
	ScaleUp      string      `json:"scaleUp"`
	ScaleDown    string      `json:"scaleDown"`
}

type TimebasedController struct {
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
		cache.NewListWatchFromClient(urc.cfg.Client.Core().RESTClient(policyEndpoint), "policies", api.NamespaceAll, fields.Everything()),
		&Policy{}, policy.cfg.ResyncPeriod, cache.ResourceEventHandlerFuncs{})

	return policy
}

func (a *NewTimebasedController) Run(stopCh <-chan struct{}) {
	// start a single worker (we may wish to start more in the future)
	go wait.Until(a.worker, time.Second, stopCh)

	<-stopCh
}

func (a *NewTimebasedController) worker() {

	pl, err := a.policyLister.Store.List()

	policies = pl.items

	for _, p := range policies {
		reconcileAutoscaler(&p, time.Now())
	}

}

func getRecentUnmetScheduleTimes(p policy, now time.Time) ([]time.Time, error) {
	starts := []time.Time{}
	sched, err := p.ParseStandard(p.Spec.Schedule)
	if err != nil {
		return starts, fmt.Errorf("Unparseable schedule: %s : %s", sj.Spec.Schedule, err)
	}

	var earliestTime time.Time
	if p.Status.LastScheduleTime != nil {
		earliestTime = p.Status.LastScheduleTime.Time
	} else {
		// If none found, then this is either a recently created scheduledJob,
		// or the active/completed info was somehow lost (contract for status
		// in kubernetes says it may need to be recreated), or that we have
		// started a job, but have not noticed it yet (distributed systems can
		// have arbitrary delays).  In any case, use the creation time of the
		// CronJob as last known start time.
		earliestTime = p.ObjectMeta.CreationTimestamp.Time
	}
	if earliestTime.After(now) {
		return []time.Time{}, nil
	}

	for t := sched.Next(earliestTime); !t.After(now); t = sched.Next(t) {
		starts = append(starts, t)
		// An object might miss several starts.  For example, if
		// controller gets wedged on friday at 5:01pm when everyone has
		// gone home, and someone comes in on tuesday AM and discovers
		// the problem and restarts the controller, then all the hourly
		// jobs, more than 80 of them for one hourly scheduledJob, should
		// all start running with no further intervention (if the scheduledJob
		// allows concurrency and late starts).
		//
		// However, if there is a bug somewhere, or incorrect clock
		// on controller's server or apiservers (for setting creationTimestamp)
		// then there could be so many missed start times (it could be off
		// by decades or more), that it would eat up all the CPU and memory
		// of this controller. In that case, we want to not try to list
		// all the misseded start times.
		//
		// I've somewhat arbitrarily picked 100, as more than 80, but
		// but less than "lots".
		if len(starts) > 100 {
			// We can't get the most recent times so just return an empty slice
			return []time.Time{}, fmt.Errorf("Too many missed start time (> 100). Set or decrease .spec.startingDeadlineSeconds or check clock skew.")
		}
	}
	return starts, nil
}

func (a *NewTimebasedController) reconcileAutoscaler(p *policy, now time.Time) error {

	reference := fmt.Sprintf("%s/%s/%s", a.Spec.ScaleTargetRef.Kind, a.Namespace, a.Spec.ScaleTargetRef.Name)

	scale, err := a.scaleNamespacer.Scales(a.Namespace).Get(a.Spec.ScaleTargetRef.Kind, a.Spec.ScaleTargetRef.Name)
	if err != nil {
		return fmt.Errorf("failed to query scale subresource for %s: %v", reference, err)
	}

	times, err := getRecentUnmetScheduleTimes(*p, now)
	if err != nil {
		glog.Errorf("Cannot determine if %s needs to be started: %v", nameForLog, err)
	}
	// TODO: handle multiple unmet start times, from oldest to newest, updating status as needed.
	if len(times) == 0 {
		glog.V(4).Infof("No unmet start times for %s", nameForLog)
		return
	}
	if len(times) > 1 {
		glog.V(4).Infof("Multiple unmet start times for %s so only starting last one", nameForLog)
	}
	scheduledTime := times[len(times)-1]

	currentReplicas := scale.Status.Replicas

	if p.Spec.Action == "scaleUp" {
		if p.Spec.Replicas <= currentReplicas {
			glog.V(4).Infof("The request replicas was less than current replicas, no need to scale up")
			return
		} else {
			scale.Spec.Replicas = p.Spec.Replicas
			_, err = p.scaleNamespacer.Scales(p.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
		}
	} else {
		if p.Spec.Replicas >= currentReplicas {
			glog.V(4).Infof("the request replicas was large than replicas, no need to scale down")
			return
		} else {
			scale.Spec.Replicas = p.Spec.Replicas
			_, err = p.scaleNamespacer.Scales(p.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
		}
	}
}

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
		&clientcmd.ConfigOverrides{ClusterInfo: api.Cluster{Server: masterUrl}}).ClientConfig()
}

func CreateApiserverClient(apiserverHost string, kubeConfig string) (*kubernetes.Clientset, *rest.Config, error) {
	cfg, err := buildConfigFromFlags(apiserverHost, kubeConfig)
	if err != nil {
		return nil, nil, err
	}

	cfg.QPS = defaultQPS
	cfg.Burst = defaultBurst
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

    log.Printf("Using HTTP port: %d", *argPort)
    if *argApiserverHost != "" {
        log.Printf("Using apiserver-host location: %s", *argApiserverHost)
    }
    if *argKubeConfigFile != "" {
        log.Printf("Using kubeconfig file: %s", *argKubeConfigFile)
    }

    apiserverClient, config, err := client.CreateApiserverClient(*argApiserverHost, *argKubeConfigFile)
    if err != nil {
        handleFatalInitError(err)
    }

    versionInfo, err := apiserverClient.ServerVersion()
    if err != nil {
        handleFatalInitError(err)
    }
    log.Printf("Successful initial request to the apiserver, version: %s", versionInfo.String())

    urc := controller.NewTimebasedController(&controller.Configuration{
        Client:       apiserverClient,
        ResyncPeriod: 5 * time.Minute,
    })

    urc.Run()
}
