package framework

import (
	"fmt"
	"time"

	"github.com/appscode/go/crypto/rand"
	"github.com/appscode/go/encoding/json/types"
	"github.com/appscode/log"
	tapi "github.com/k8sdb/apimachinery/api"
	. "github.com/onsi/gomega"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Invocation) Postgres() *tapi.Postgres {
	return &tapi.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rand.WithUniqSuffix("postgres"),
			Namespace: f.namespace,
			Labels: map[string]string{
				"app": f.app,
			},
		},
		Spec: tapi.PostgresSpec{
			Version: types.StrYo("9.5"),
		},
	}
}

func (f *Framework) CreatePostgres(obj *tapi.Postgres) error {
	_, err := f.extClient.Postgreses(obj.Namespace).Create(obj)
	return err
}

func (f *Framework) GetPostgres(meta metav1.ObjectMeta) (*tapi.Postgres, error) {
	return f.extClient.Postgreses(meta.Namespace).Get(meta.Name)
}

func (f *Framework) UpdatePostgres(meta metav1.ObjectMeta, transformer func(tapi.Postgres) tapi.Postgres) (*tapi.Postgres, error) {
	attempt := 0
	for ; attempt < maxAttempts; attempt = attempt + 1 {
		cur, err := f.extClient.Postgreses(meta.Namespace).Get(meta.Name)
		if err != nil {
			return nil, err
		}

		modified := transformer(*cur)
		updated, err := f.extClient.Postgreses(cur.Namespace).Update(&modified)
		if err == nil {
			return updated, nil
		}

		log.Errorf("Attempt %d failed to update Postgres %s@%s due to %s.", attempt, cur.Name, cur.Namespace, err)
		time.Sleep(updateRetryInterval)
	}

	return nil, fmt.Errorf("Failed to update Postgres %s@%s after %d attempts.", meta.Name, meta.Namespace, attempt)
}

func (f *Framework) DeletePostgres(meta metav1.ObjectMeta) error {
	return f.extClient.Postgreses(meta.Namespace).Delete(meta.Name)
}

func (f *Framework) EventuallyPostgres(meta metav1.ObjectMeta) GomegaAsyncAssertion {
	return Eventually(
		func() bool {
			if _, err := f.extClient.Postgreses(meta.Namespace).Get(meta.Name); kerr.IsNotFound(err) {
				return false
			}
			return true
		},
		time.Minute*5,
		time.Second*5,
	)
}

func (f *Framework) EventuallyPostgresRunning(meta metav1.ObjectMeta) GomegaAsyncAssertion {
	return Eventually(
		func() bool {
			postgres, err := f.extClient.Postgreses(meta.Namespace).Get(meta.Name)
			Expect(err).NotTo(HaveOccurred())
			return postgres.Status.Phase == tapi.DatabasePhaseRunning
		},
		time.Minute*5,
		time.Second*5,
	)
}
