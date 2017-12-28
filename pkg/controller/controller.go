package controller

import (
	"time"

	"github.com/appscode/go/hold"
	"github.com/appscode/go/log"
	apiext_util "github.com/appscode/kutil/apiextensions/v1beta1"
	pcm "github.com/coreos/prometheus-operator/pkg/client/monitoring/v1"
	api "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	cs "github.com/kubedb/apimachinery/client/typed/kubedb/v1alpha1"
	kutildb "github.com/kubedb/apimachinery/client/typed/kubedb/v1alpha1/util"
	amc "github.com/kubedb/apimachinery/pkg/controller"
	drmnc "github.com/kubedb/apimachinery/pkg/controller/dormant_database"
	snapc "github.com/kubedb/apimachinery/pkg/controller/snapshot"
	"github.com/kubedb/apimachinery/pkg/eventer"
	"github.com/kubedb/postgres/pkg/docker"
	core "k8s.io/api/core/v1"
	crd_api "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

type Options struct {
	Docker docker.Docker
	// Operator namespace
	OperatorNamespace string
	// Governing service
	GoverningService string
	// Address to listen on for web interface and telemetry.
	Address string
	// Enable RBAC for database workloads
	EnableRbac bool
	//Max number requests for retries
	MaxNumRequeues int
}

type Controller struct {
	*amc.Controller
	// Prometheus client
	promClient pcm.MonitoringV1Interface
	// Cron Controller
	cronController snapc.CronControllerInterface
	// Event Recorder
	recorder record.EventRecorder
	// Flag data
	opt Options
	// sync time to sync the list.
	syncPeriod time.Duration

	// Workqueue
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
}

var _ snapc.Snapshotter = &Controller{}
var _ drmnc.Deleter = &Controller{}

func New(
	client kubernetes.Interface,
	apiExtKubeClient crd_cs.ApiextensionsV1beta1Interface,
	extClient cs.KubedbV1alpha1Interface,
	promClient pcm.MonitoringV1Interface,
	cronController snapc.CronControllerInterface,
	opt Options,
) *Controller {
	return &Controller{
		Controller: &amc.Controller{
			Client:           client,
			ExtClient:        extClient,
			ApiExtKubeClient: apiExtKubeClient,
		},
		promClient:     promClient,
		cronController: cronController,
		recorder:       eventer.NewEventRecorder(client, "Postgres operator"),
		opt:            opt,
		syncPeriod:     time.Minute * 2,
	}
}

// Ensuring Custom Resource Definitions
func (c *Controller) Setup() error {
	log.Infoln("Ensuring CustomResourceDefinition...")
	crds := []*crd_api.CustomResourceDefinition{
		api.Postgres{}.CustomResourceDefinition(),
		api.DormantDatabase{}.CustomResourceDefinition(),
		api.Snapshot{}.CustomResourceDefinition(),
	}
	return apiext_util.RegisterCRDs(c.ApiExtKubeClient, crds)
}

func (c *Controller) Run() {
	// Watch Postgres TPR objects
	go c.watchPostgres()
	// Watch Snapshot with labelSelector only for Postgres
	go c.watchSnapshot()
	// Watch DormantDatabase with labelSelector only for Postgres
	go c.watchDormantDatabase()
}

// Blocks caller. Intended to be called as a Go routine.
func (c *Controller) RunAndHold() {
	c.Run()

	// Run HTTP server to expose metrics, audit endpoint & debug profiles.
	go c.runHTTPServer()
	// hold
	hold.Hold()
}

func (c *Controller) watchPostgres() {
	c.initWatcher()

	stop := make(chan struct{})
	defer close(stop)

	c.runWatcher(1, stop)
	select {}
}

func (c *Controller) watchSnapshot() {
	labelMap := map[string]string{
		api.LabelDatabaseKind: api.ResourceKindPostgres,
	}
	// Watch with label selector
	lw := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return c.ExtClient.Snapshots(metav1.NamespaceAll).List(
				metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelMap).String(),
				})
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return c.ExtClient.Snapshots(metav1.NamespaceAll).Watch(
				metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelMap).String(),
				})
		},
	}

	snapc.NewController(c.Controller, c, lw, c.syncPeriod).Run()
}

func (c *Controller) watchDormantDatabase() {
	labelMap := map[string]string{
		api.LabelDatabaseKind: api.ResourceKindPostgres,
	}
	// Watch with label selector
	lw := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return c.ExtClient.DormantDatabases(metav1.NamespaceAll).List(
				metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelMap).String(),
				})
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return c.ExtClient.DormantDatabases(metav1.NamespaceAll).Watch(
				metav1.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelMap).String(),
				})
		},
	}

	drmnc.NewController(c.Controller, c, lw, c.syncPeriod).Run()
}

func (c *Controller) pushFailureEvent(postgres *api.Postgres, reason string) {
	c.recorder.Eventf(
		postgres.ObjectReference(),
		core.EventTypeWarning,
		eventer.EventReasonFailedToStart,
		`Fail to be ready Postgres: "%v". Reason: %v`,
		postgres.Name,
		reason,
	)

	pg, _, err := kutildb.PatchPostgres(c.ExtClient, postgres, func(in *api.Postgres) *api.Postgres {
		in.Status.Phase = api.DatabasePhaseFailed
		in.Status.Reason = reason
		return in
	})
	if err != nil {
		c.recorder.Eventf(postgres.ObjectReference(), core.EventTypeWarning, eventer.EventReasonFailedToUpdate, err.Error())
	}
	postgres.Status = pg.Status
}
