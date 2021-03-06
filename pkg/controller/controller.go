package controller

import (
	"fmt"
	"time"

	"github.com/golang/glog"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/robfig/cron"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	extensionsclient "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	api "github.com/hchenxa/timebase/pkg/api/icp.ibm.com/v1"
)

// PolicyLister is to store list of policies
type PolicyLister struct {
	cache.Store
}

// Configuration is the controller configuration
type Configuration struct {
	RESTClient   *rest.RESTClient
	Client       *kubernetes.Clientset
	Scheme       *runtime.Scheme
	ResyncPeriod time.Duration
}

// TimebasedController is the controller for time based auto scaling
type TimebasedController struct {
	cfg *Configuration

	scaleNamespacer  extensionsclient.ScalesGetter
	policyController cache.Controller
	policyLister     PolicyLister

	stopCh chan struct{}
}

// NewTimebasedController create a new controller
func NewTimebasedController(config *Configuration) *TimebasedController {
	policy := TimebasedController{
		cfg:    config,
		stopCh: make(chan struct{}),
	}

	policy.scaleNamespacer = policy.cfg.Client.Extensions()

	policy.policyLister.Store, policy.policyController = cache.NewInformer(
		cache.NewListWatchFromClient(policy.cfg.RESTClient, "policies", v1.NamespaceAll, fields.Everything()),
		&api.Policy{}, policy.cfg.ResyncPeriod, cache.ResourceEventHandlerFuncs{})

	return &policy
}

// Run method start the controller
func (a *TimebasedController) Run(stopCh <-chan struct{}) {
	// Start controller
	go a.policyController.Run(stopCh)
	// start a single worker (we may wish to start more in the future)
	go wait.Until(a.worker, time.Second, stopCh)

	<-stopCh
}

func (a *TimebasedController) worker() {

	pl := a.policyLister.Store.List()

	// policies := pl.Items
	// glog.Infof("the policy list was: %s", pl.string)
	for _, pIf := range pl {
		p := pIf.(*api.Policy)
		a.reconcileAutoscaler(p, time.Now())
	}

}

func getRecentUnmetScheduleTimes(p *api.Policy, now time.Time) ([]time.Time, error) {
	starts := []time.Time{}
	sched, err := cron.ParseStandard(p.Spec.Schedule)
	if err != nil {
		return starts, fmt.Errorf("Unparseable schedule: %s : %s", p.Spec.Schedule, err)
	}

	var earliestTime time.Time
	if p.Spec.Status.LastScheduleTime != nil {
		earliestTime = p.Spec.Status.LastScheduleTime.Time
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
			return []time.Time{}, fmt.Errorf("Too many missed start time (> 100), set or decrease .spec.startingDeadlineSeconds or check clock skew")
		}
	}
	return starts, nil
}

func (a *TimebasedController) reconcileAutoscaler(p *api.Policy, now time.Time) {

	reference := fmt.Sprintf("%s/%s/%s", p.Spec.ScaleTargetRef.Kind, p.ObjectMeta.Namespace, p.Spec.ScaleTargetRef.Name)

	scale, err := a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Get(p.Spec.ScaleTargetRef.Kind, p.Spec.ScaleTargetRef.Name)
	if err != nil {
		glog.Errorf("failed to query scale subresource for %s: %v", reference, err)
		return
	}

	times, err := getRecentUnmetScheduleTimes(p, now)
	if err != nil {
		glog.Errorf("Cannot determine needs to be started: %v", err)
	}
	// TODO: handle multiple unmet start times, from oldest to newest, updating status as needed.
	if len(times) <= 0 {
		glog.V(4).Infof("No unmet start times")
		return
	}

	glog.V(4).Infof("Multiple unmet start times so only starting last one")

	currentReplicas := scale.Status.Replicas

	if p.Spec.Action == api.ScaleUp {
		if p.Spec.TargetReplicas <= currentReplicas {
			glog.V(4).Infof("The request replicas was less than current replicas, no need to scale up")
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				glog.Errorf("failed to rescale %s: %v", reference, err)
				return
			}
			p.Spec.Status.LastScheduleTime = &metav1.Time{Time: now}
		}
	} else {
		if p.Spec.TargetReplicas >= currentReplicas {
			glog.V(4).Infof("the request replicas was large than replicas, no need to scale down")
		} else {
			scale.Spec.Replicas = p.Spec.TargetReplicas
			_, err = a.scaleNamespacer.Scales(p.ObjectMeta.Namespace).Update(p.Spec.ScaleTargetRef.Kind, scale)
			if err != nil {
				glog.Errorf("failed to rescale %s: %v", reference, err)
				return
			}
			p.Spec.Status.LastScheduleTime = &metav1.Time{Time: now}
		}
	}
}
