package monitor

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/coreos/prometheus-operator/pkg/client/monitoring/v1alpha1"
	_ "github.com/coreos/prometheus-operator/pkg/client/monitoring/v1alpha1"
	prom "github.com/coreos/prometheus-operator/pkg/client/monitoring/v1alpha1"
	tapi "github.com/k8sdb/apimachinery/api"
	cgerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
)

type PrometheusController struct {
	kubeClient        clientset.Interface
	promClient        v1alpha1.MonitoringV1alpha1Interface
	operatorNamespace string
}

func NewPrometheusController(kubeClient clientset.Interface, promClient v1alpha1.MonitoringV1alpha1Interface, operatorNamespace string) Monitor {
	return &PrometheusController{
		kubeClient:        kubeClient,
		promClient:        promClient,
		operatorNamespace: operatorNamespace,
	}
}

func (c *PrometheusController) AddMonitor(meta metav1.ObjectMeta, spec *tapi.MonitorSpec) error {
	if !c.SupportsCoreOSOperator() {
		return errors.New("Cluster does not support CoreOS Prometheus operator")
	}
	return c.ensureServiceMonitor(meta, spec, spec)
}

func (c *PrometheusController) UpdateMonitor(meta metav1.ObjectMeta, old, new *tapi.MonitorSpec) error {
	if !c.SupportsCoreOSOperator() {
		return errors.New("Cluster does not support CoreOS Prometheus operator")
	}
	return c.ensureServiceMonitor(meta, old, new)
}

func (c *PrometheusController) DeleteMonitor(meta metav1.ObjectMeta, spec *tapi.MonitorSpec) error {
	if !c.SupportsCoreOSOperator() {
		return errors.New("Cluster does not support CoreOS Prometheus operator")
	}
	if err := c.promClient.ServiceMonitors(spec.Prometheus.Namespace).Delete(getServiceMonitorName(meta), nil); !cgerr.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *PrometheusController) SupportsCoreOSOperator() bool {
	_, err := c.kubeClient.ExtensionsV1beta1().ThirdPartyResources().Get("prometheus."+prom.TPRGroup, metav1.GetOptions{})
	if err != nil {
		return false
	}
	_, err = c.kubeClient.ExtensionsV1beta1().ThirdPartyResources().Get("service-monitor."+prom.TPRGroup, metav1.GetOptions{})
	if err != nil {
		return false
	}
	return true
}

func (c *PrometheusController) ensureServiceMonitor(meta metav1.ObjectMeta, old, new *tapi.MonitorSpec) error {
	name := getServiceMonitorName(meta)
	if old != nil && (new == nil || old.Prometheus.Namespace != new.Prometheus.Namespace) {
		err := c.promClient.ServiceMonitors(old.Prometheus.Namespace).Delete(name, nil)
		if err != nil && !cgerr.IsNotFound(err) {
			return err
		}
		if new == nil {
			return nil
		}
	}

	actual, err := c.promClient.ServiceMonitors(new.Prometheus.Namespace).Get(name)
	if cgerr.IsNotFound(err) {
		return c.createServiceMonitor(meta, new)
	} else if err != nil {
		return err
	}

	update := false
	if !reflect.DeepEqual(actual.Labels, new.Prometheus.Labels) {
		update = true
	}

	if !update {
		for _, e := range actual.Spec.Endpoints {
			if e.Interval != new.Prometheus.Interval {
				update = true
				break
			}
		}
	}

	if update {
		actual.Labels = new.Prometheus.Labels
		for i := range actual.Spec.Endpoints {
			actual.Spec.Endpoints[i].Interval = new.Prometheus.Interval
		}
		_, err := c.promClient.ServiceMonitors(new.Prometheus.Namespace).Update(actual)
		return err
	}

	return nil
}

func (c *PrometheusController) createServiceMonitor(meta metav1.ObjectMeta, spec *tapi.MonitorSpec) error {
	svc, err := c.kubeClient.CoreV1().Services(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	ports := svc.Spec.Ports
	if len(ports) == 0 {
		return errors.New("No port found in database service")
	}

	sm := &prom.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getServiceMonitorName(meta),
			Namespace: spec.Prometheus.Namespace,
			Labels:    spec.Prometheus.Labels,
		},
		Spec: prom.ServiceMonitorSpec{
			NamespaceSelector: prom.NamespaceSelector{
				MatchNames: []string{svc.Namespace},
			},
			Endpoints: []prom.Endpoint{
				{
					TargetPort: spec.Prometheus.TargetPort,
					Interval:   spec.Prometheus.Interval,
					Path:       fmt.Sprintf("/kubedb.com/v1alpha1/namespaces/%s/%s/%s/metrics", meta.Namespace, getTypeFromSelfLink(meta.SelfLink), meta.Name),
				},
			},
			Selector: metav1.LabelSelector{
				MatchLabels: svc.Spec.Selector,
			},
		},
	}
	if _, err := c.promClient.ServiceMonitors(spec.Prometheus.Namespace).Create(sm); !cgerr.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func getTypeFromSelfLink(selfLink string) string {
	if len(selfLink) == 0 {
		return ""
	}
	s := strings.Split(selfLink, "/")
	return s[len(s)-2]
}

func getServiceMonitorName(meta metav1.ObjectMeta) string {
	return fmt.Sprintf("kubedb-%s-%s", meta.Namespace, meta.Name)
}
