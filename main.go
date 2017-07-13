package main

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/robfig/cron"
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	extensionsclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/extensions/v1beta1"
	autoscalingclient "k8s.io/kubernetes/pkg/client/clientset_generated/clientset/typed/autoscaling/v1"
	"k8s.io/client-go/rest"
	"github.com/spf13/pflag"

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
		cache.NewListWatchFromClient(policy.cfg.Client.Apps().RESTClient(), "policies", api.NamespaceAll, fields.Everything()),
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
	sched, err := cron.ParseStandard(p.Spec.Schedule)
	if err != nil {
		return starts, fmt.Errorf("Unparseable schedule: %s : %s", sj.Spec.Schedule, err)
	}

	var earliestTime time.Time
	if p.Status.LastScheduleTime != nil {
		earliestTime = p.Status.LastScheduleTime.Time
	} else {

		earliestTime = p.ObjectMeta.CreationTimestamp.Time
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

	currentReplicas := scale.Status.Replicas

	if p.Spec.Action == "scaleUp" {
		if p.Spec.TargetReplicas <= currentReplicas {
			glog.V(4).Infof("The request replicas was less than current replicas, no need to scale up")
			return
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = p.scaleNamespacer.Scales(p.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
		}
	} else {
		if p.Spec.TargetReplicas >= currentReplicas {
			glog.V(4).Infof("the request replicas was large than replicas, no need to scale down")
			return
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = p.scaleNamespacer.Scales(p.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				return fmt.Errorf("failed to rescale %s: %v", reference, err)
			}
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

    log.Printf("Using HTTP port: %d", *argPort)
    if *argApiserverHost != "" {
        log.Printf("Using apiserver-host location: %s", *argApiserverHost)
    }
    if *argKubeConfigFile != "" {
        log.Printf("Using kubeconfig file: %s", *argKubeConfigFile)
    }

    apiserverClient, config, err := CreateApiserverClient()
    if err != nil {
        handleFatalInitError(err)
    }

    versionInfo, err := apiserverClient.ServerVersion()
    if err != nil {
        handleFatalInitError(err)
    }
    log.Printf("Successful initial request to the apiserver, version: %s", versionInfo.String())

    pc := NewTimebasedController(&controller.Configuration{
        Client:       apiserverClient,
        ResyncPeriod: 5 * time.Minute,
    })

    pc.Run()
}
