package util

import (
	"encoding/json"
	"fmt"

	"github.com/appscode/kutil"
	"github.com/golang/glog"
	api "github.com/k8sdb/apimachinery/apis/kubedb/v1alpha1"
	cs "github.com/k8sdb/apimachinery/client/typed/kubedb/v1alpha1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"k8s.io/apimachinery/pkg/util/wait"
)

func EnsureElasticsearch(c cs.KubedbV1alpha1Interface, meta metav1.ObjectMeta, transform func(alert *api.Elasticsearch) *api.Elasticsearch) (*api.Elasticsearch, error) {
	return CreateOrPatchElasticsearch(c, meta, transform)
}

func CreateOrPatchElasticsearch(c cs.KubedbV1alpha1Interface, meta metav1.ObjectMeta, transform func(alert *api.Elasticsearch) *api.Elasticsearch) (*api.Elasticsearch, error) {
	cur, err := c.Elasticsearchs(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
	if kerr.IsNotFound(err) {
		glog.V(3).Infof("Creating Elasticsearch %s/%s.", meta.Namespace, meta.Name)
		return c.Elasticsearchs(meta.Namespace).Create(transform(&api.Elasticsearch{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Elasticsearch",
				APIVersion: api.SchemeGroupVersion.String(),
			},
			ObjectMeta: meta,
		}))
	} else if err != nil {
		return nil, err
	}
	return PatchElasticsearch(c, cur, transform)
}

func PatchElasticsearch(c cs.KubedbV1alpha1Interface, cur *api.Elasticsearch, transform func(*api.Elasticsearch) *api.Elasticsearch) (*api.Elasticsearch, error) {
	curJson, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}

	modJson, err := json.Marshal(transform(cur.DeepCopy()))
	if err != nil {
		return nil, err
	}

	patch, err := jsonmergepatch.CreateThreeWayJSONMergePatch(curJson, modJson, curJson)
	if err != nil {
		return nil, err
	}
	if len(patch) == 0 || string(patch) == "{}" {
		return cur, nil
	}
	glog.V(3).Infof("Patching Elasticsearch %s/%s with %s.", cur.Namespace, cur.Name, string(patch))
	result, err := c.Elasticsearchs(cur.Namespace).Patch(cur.Name, types.MergePatchType, patch)
	return result, err
}

func TryPatchElasticsearch(c cs.KubedbV1alpha1Interface, meta metav1.ObjectMeta, transform func(*api.Elasticsearch) *api.Elasticsearch) (result *api.Elasticsearch, err error) {
	attempt := 0
	err = wait.PollImmediate(kutil.RetryInterval, kutil.RetryTimeout, func() (bool, error) {
		attempt++
		cur, e2 := c.Elasticsearchs(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
		if kerr.IsNotFound(e2) {
			return false, e2
		} else if e2 == nil {
			result, e2 = PatchElasticsearch(c, cur, transform)
			return e2 == nil, nil
		}
		glog.Errorf("Attempt %d failed to patch Elasticsearch %s/%s due to %v.", attempt, cur.Namespace, cur.Name, e2)
		return false, nil
	})

	if err != nil {
		err = fmt.Errorf("failed to patch Elasticsearch %s/%s after %d attempts due to %v", meta.Namespace, meta.Name, attempt, err)
	}
	return
}

func TryUpdateElasticsearch(c cs.KubedbV1alpha1Interface, meta metav1.ObjectMeta, transform func(*api.Elasticsearch) *api.Elasticsearch) (result *api.Elasticsearch, err error) {
	attempt := 0
	err = wait.PollImmediate(kutil.RetryInterval, kutil.RetryTimeout, func() (bool, error) {
		attempt++
		cur, e2 := c.Elasticsearchs(meta.Namespace).Get(meta.Name, metav1.GetOptions{})
		if kerr.IsNotFound(e2) {
			return false, e2
		} else if e2 == nil {
			result, e2 = c.Elasticsearchs(cur.Namespace).Update(transform(cur.DeepCopy()))
			return e2 == nil, nil
		}
		glog.Errorf("Attempt %d failed to update Elasticsearch %s/%s due to %v.", attempt, cur.Namespace, cur.Name, e2)
		return false, nil
	})

	if err != nil {
		err = fmt.Errorf("failed to update Elasticsearch %s/%s after %d attempts due to %v", meta.Namespace, meta.Name, attempt, err)
	}
	return
}
