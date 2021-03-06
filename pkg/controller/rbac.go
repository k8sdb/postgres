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

package controller

import (
	"context"

	"kubedb.dev/apimachinery/apis/kubedb"
	api "kubedb.dev/apimachinery/apis/kubedb/v1alpha2"

	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	policy_v1beta1 "k8s.io/api/policy/v1beta1"
	rbac "k8s.io/api/rbac/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	rbac_util "kmodules.xyz/client-go/rbac/v1"
)

func (c *Controller) ensureRole(db *api.Postgres, pspName string) error {
	owner := metav1.NewControllerRef(db, api.SchemeGroupVersion.WithKind(api.ResourceKindPostgres))

	// Create new Roles
	_, _, err := rbac_util.CreateOrPatchRole(
		context.TODO(),
		c.Client,
		metav1.ObjectMeta{
			Name:      db.OffshootName(),
			Namespace: db.Namespace,
		},
		func(in *rbac.Role) *rbac.Role {
			core_util.EnsureOwnerReference(&in.ObjectMeta, owner)
			in.Labels = db.OffshootLabels()
			in.Rules = []rbac.PolicyRule{
				{
					APIGroups:     []string{apps.GroupName},
					Resources:     []string{"statefulsets"},
					Verbs:         []string{"get"},
					ResourceNames: []string{db.OffshootName()},
				},
				{
					APIGroups: []string{core.GroupName},
					Resources: []string{"pods"},
					Verbs:     []string{"get", "list", "patch", "delete"},
				},
				{
					APIGroups: []string{core.GroupName},
					Resources: []string{"pods/exec"},
					Verbs:     []string{"create"},
				},
				{
					APIGroups: []string{core.GroupName},
					Resources: []string{"secrets"},
					Verbs:     []string{"get", "list"},
				},
				{
					APIGroups: []string{core.GroupName},
					Resources: []string{"configmaps"},
					Verbs:     []string{"create", "get", "update"},
				},
			}
			if pspName != "" {
				pspRule := rbac.PolicyRule{
					APIGroups:     []string{policy_v1beta1.GroupName},
					Resources:     []string{"podsecuritypolicies"},
					Verbs:         []string{"use"},
					ResourceNames: []string{pspName},
				}
				in.Rules = append(in.Rules, pspRule)
			}
			return in
		},
		metav1.PatchOptions{},
	)
	return err
}

func (c *Controller) createServiceAccount(db *api.Postgres, saName string) error {
	owner := metav1.NewControllerRef(db, api.SchemeGroupVersion.WithKind(api.ResourceKindPostgres))

	// Create new ServiceAccount
	_, _, err := core_util.CreateOrPatchServiceAccount(
		context.TODO(),
		c.Client,
		metav1.ObjectMeta{
			Name:      saName,
			Namespace: db.Namespace,
		},
		func(in *core.ServiceAccount) *core.ServiceAccount {
			core_util.EnsureOwnerReference(&in.ObjectMeta, owner)
			in.Labels = db.OffshootLabels()
			return in
		},
		metav1.PatchOptions{},
	)
	return err
}

func (c *Controller) createRoleBinding(db *api.Postgres, saName string) error {
	owner := metav1.NewControllerRef(db, api.SchemeGroupVersion.WithKind(api.ResourceKindPostgres))

	// Ensure new RoleBindings
	_, _, err := rbac_util.CreateOrPatchRoleBinding(
		context.TODO(),
		c.Client,
		metav1.ObjectMeta{
			Name:      db.OffshootName(),
			Namespace: db.Namespace,
		},
		func(in *rbac.RoleBinding) *rbac.RoleBinding {
			core_util.EnsureOwnerReference(&in.ObjectMeta, owner)
			in.Labels = db.OffshootLabels()
			in.RoleRef = rbac.RoleRef{
				APIGroup: rbac.GroupName,
				Kind:     "Role",
				Name:     db.OffshootName(),
			}
			in.Subjects = []rbac.Subject{
				{
					Kind:      rbac.ServiceAccountKind,
					Name:      saName,
					Namespace: db.Namespace,
				},
			}
			return in
		},
		metav1.PatchOptions{},
	)
	return err
}

func (c *Controller) getPolicyNames(db *api.Postgres) (string, error) {
	dbVersion, err := c.DBClient.CatalogV1alpha1().PostgresVersions().Get(context.TODO(), db.Spec.Version, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	dbPolicyName := dbVersion.Spec.PodSecurityPolicies.DatabasePolicyName

	return dbPolicyName, nil
}

func (c *Controller) ensureDatabaseRBAC(db *api.Postgres) error {
	saName := db.Spec.PodTemplate.Spec.ServiceAccountName
	if saName == "" {
		saName = db.OffshootName()
		db.Spec.PodTemplate.Spec.ServiceAccountName = saName
	}

	sa, err := c.Client.CoreV1().ServiceAccounts(db.Namespace).Get(context.TODO(), saName, metav1.GetOptions{})
	if kerr.IsNotFound(err) {
		// create service account, since it does not exist
		if err = c.createServiceAccount(db, saName); err != nil {
			if !kerr.IsAlreadyExists(err) {
				return err
			}
		}
	} else if err != nil {
		return err
	} else if sa.Labels[meta_util.ManagedByLabelKey] != kubedb.GroupName {
		// user provided the service account, so do nothing.
		return nil
	}

	// Create New Role
	pspName, err := c.getPolicyNames(db)
	if err != nil {
		return err
	}
	if err := c.ensureRole(db, pspName); err != nil {
		return err
	}

	// Create New RoleBinding
	if err := c.createRoleBinding(db, saName); err != nil {
		return err
	}

	return nil
}
