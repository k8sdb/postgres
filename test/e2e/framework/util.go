/*
Copyright AppsCode Inc. and Contributors

Licensed under the AppsCode Community License 1.0.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://github.com/appscode/licenses/raw/1.0.0/AppsCode-Community-1.0.0.md

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"context"
	"fmt"
	"time"

	"kubedb.dev/apimachinery/apis/kubedb"
	api "kubedb.dev/apimachinery/apis/kubedb/v1alpha2"

	. "github.com/onsi/gomega"
	shell "gomodules.xyz/go-sh"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	meta_util "kmodules.xyz/client-go/meta"
)

const (
	updateRetryInterval = 10 * 1000 * 1000 * time.Nanosecond
	maxAttempts         = 5
)

func (f *Framework) CleanWorkloadLeftOvers() {
	// delete statefulset
	if err := f.kubeClient.AppsV1().StatefulSets(f.namespace).DeleteCollection(context.TODO(), meta_util.DeleteInForeground(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			meta_util.NameLabelKey:      api.Postgres{}.ResourceFQN(),
			meta_util.ManagedByLabelKey: kubedb.GroupName,
		}).String(),
	}); err != nil && !kerr.IsNotFound(err) {
		fmt.Printf("error in deletion of Statefulset. Error: %v", err)
	}

	// delete pvc
	if err := f.kubeClient.CoreV1().PersistentVolumeClaims(f.namespace).DeleteCollection(context.TODO(), meta_util.DeleteInForeground(), metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			meta_util.NameLabelKey:      api.Postgres{}.ResourceFQN(),
			meta_util.ManagedByLabelKey: kubedb.GroupName,
		}).String(),
	}); err != nil && !kerr.IsNotFound(err) {
		fmt.Printf("error in deletion of PVC. Error: %v", err)
	}
}

func (f *Framework) PrintDebugHelpers() {
	sh := shell.NewSession()

	fmt.Println("\n======================================[ Describe Job ]===================================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "job", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe Pod ]===================================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "po", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe BackupSession ]==========================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "backupsession", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe RestoreSession ]==========================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "restoresession", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe Snapshots ]===================================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "snapshot", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe Postgres ]===================================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "pg", "-n", f.Namespace()).Run(); err != nil {
		fmt.Println(err)
	}
	fmt.Println("\n======================================[ Describe Nodes ]===================================================")
	if err := sh.Command("/usr/bin/kubectl", "describe", "nodes").Run(); err != nil {
		fmt.Println(err)
	}
}

func (f *Framework) EventuallyWipedOut(meta metav1.ObjectMeta) GomegaAsyncAssertion {
	return Eventually(
		func() error {
			labelMap := map[string]string{
				meta_util.NameLabelKey:      api.Postgres{}.ResourceFQN(),
				meta_util.InstanceLabelKey:  meta.Name,
				meta_util.ManagedByLabelKey: kubedb.GroupName,
			}
			labelSelector := labels.SelectorFromSet(labelMap)

			// check if pvcs is wiped out
			pvcList, err := f.kubeClient.CoreV1().PersistentVolumeClaims(meta.Namespace).List(
				context.TODO(),
				metav1.ListOptions{
					LabelSelector: labelSelector.String(),
				},
			)
			if err != nil {
				return err
			}
			if len(pvcList.Items) > 0 {
				fmt.Println("PVCs have not wiped out yet")
				return fmt.Errorf("PVCs have not wiped out yet")
			}

			// check if secrets are wiped out
			secretList, err := f.kubeClient.CoreV1().Secrets(meta.Namespace).List(
				context.TODO(),
				metav1.ListOptions{
					LabelSelector: labelSelector.String(),
				},
			)
			if err != nil {
				return err
			}
			if len(secretList.Items) > 0 {
				fmt.Println("secrets have not wiped out yet")
				return fmt.Errorf("secrets have not wiped out yet")
			}

			// check if appbinds are wiped out
			appBindingList, err := f.appCatalogClient.AppBindings(meta.Namespace).List(
				context.TODO(),
				metav1.ListOptions{
					LabelSelector: labelSelector.String(),
				},
			)
			if err != nil {
				return err
			}
			if len(appBindingList.Items) > 0 {
				fmt.Println("appBindings have not wiped out yet")
				return fmt.Errorf("appBindings have not wiped out yet")
			}

			return nil
		},
		time.Minute*10,
		time.Second*5,
	)
}
