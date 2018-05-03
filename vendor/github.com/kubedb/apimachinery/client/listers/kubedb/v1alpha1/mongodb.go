/*
Copyright 2018 The KubeDB Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// MongoDBLister helps list MongoDBs.
type MongoDBLister interface {
	// List lists all MongoDBs in the indexer.
	List(selector labels.Selector) (ret []*v1alpha1.MongoDB, err error)
	// MongoDBs returns an object that can list and get MongoDBs.
	MongoDBs(namespace string) MongoDBNamespaceLister
	MongoDBListerExpansion
}

// mongoDBLister implements the MongoDBLister interface.
type mongoDBLister struct {
	indexer cache.Indexer
}

// NewMongoDBLister returns a new MongoDBLister.
func NewMongoDBLister(indexer cache.Indexer) MongoDBLister {
	return &mongoDBLister{indexer: indexer}
}

// List lists all MongoDBs in the indexer.
func (s *mongoDBLister) List(selector labels.Selector) (ret []*v1alpha1.MongoDB, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.MongoDB))
	})
	return ret, err
}

// MongoDBs returns an object that can list and get MongoDBs.
func (s *mongoDBLister) MongoDBs(namespace string) MongoDBNamespaceLister {
	return mongoDBNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// MongoDBNamespaceLister helps list and get MongoDBs.
type MongoDBNamespaceLister interface {
	// List lists all MongoDBs in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1alpha1.MongoDB, err error)
	// Get retrieves the MongoDB from the indexer for a given namespace and name.
	Get(name string) (*v1alpha1.MongoDB, error)
	MongoDBNamespaceListerExpansion
}

// mongoDBNamespaceLister implements the MongoDBNamespaceLister
// interface.
type mongoDBNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all MongoDBs in the indexer for a given namespace.
func (s mongoDBNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.MongoDB, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.MongoDB))
	})
	return ret, err
}

// Get retrieves the MongoDB from the indexer for a given namespace and name.
func (s mongoDBNamespaceLister) Get(name string) (*v1alpha1.MongoDB, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("mongodb"), name)
	}
	return obj.(*v1alpha1.MongoDB), nil
}
