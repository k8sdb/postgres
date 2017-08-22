package framework

import (
	"time"

	kutildb "github.com/appscode/kutil/kubedb/v1alpha1"
	tapi "github.com/k8sdb/apimachinery/api"
	. "github.com/onsi/gomega"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) GetDormantDatabase(meta metav1.ObjectMeta) (*tapi.DormantDatabase, error) {
	return f.extClient.DormantDatabases(meta.Namespace).Get(meta.Name)
}

func (f *Framework) TryPatchDormantDatabase(meta metav1.ObjectMeta, transform func(*tapi.DormantDatabase) *tapi.DormantDatabase) (*tapi.DormantDatabase, error) {
	return kutildb.TryPatchDormantDatabase(f.extClient, meta, transform)
}

func (f *Framework) DeleteDormantDatabase(meta metav1.ObjectMeta) error {
	return f.extClient.DormantDatabases(meta.Namespace).Delete(meta.Name)
}

func (f *Framework) EventuallyDormantDatabase(meta metav1.ObjectMeta) GomegaAsyncAssertion {
	return Eventually(
		func() bool {
			_, err := f.extClient.DormantDatabases(meta.Namespace).Get(meta.Name)
			if err != nil {
				if kerr.IsNotFound(err) {
					return false
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}
			return true
		},
		time.Minute*5,
		time.Second*5,
	)
}

func (f *Framework) EventuallyDormantDatabaseStatus(meta metav1.ObjectMeta) GomegaAsyncAssertion {
	return Eventually(
		func() tapi.DormantDatabasePhase {
			drmn, err := f.extClient.DormantDatabases(meta.Namespace).Get(meta.Name)
			if err != nil {
				if !kerr.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
				return tapi.DormantDatabasePhase("")
			}
			return drmn.Status.Phase
		},
		time.Minute*5,
		time.Second*5,
	)
}
