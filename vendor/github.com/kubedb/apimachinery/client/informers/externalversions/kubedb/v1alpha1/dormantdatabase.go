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

// This file was automatically generated by informer-gen

package v1alpha1

import (
	time "time"

	kubedb_v1alpha1 "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	versioned "github.com/kubedb/apimachinery/client/clientset/versioned"
	internalinterfaces "github.com/kubedb/apimachinery/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/kubedb/apimachinery/client/listers/kubedb/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// DormantDatabaseInformer provides access to a shared informer and lister for
// DormantDatabases.
type DormantDatabaseInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.DormantDatabaseLister
}

type dormantDatabaseInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewDormantDatabaseInformer constructs a new informer for DormantDatabase type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewDormantDatabaseInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredDormantDatabaseInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredDormantDatabaseInformer constructs a new informer for DormantDatabase type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredDormantDatabaseInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.KubedbV1alpha1().DormantDatabases(namespace).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.KubedbV1alpha1().DormantDatabases(namespace).Watch(options)
			},
		},
		&kubedb_v1alpha1.DormantDatabase{},
		resyncPeriod,
		indexers,
	)
}

func (f *dormantDatabaseInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredDormantDatabaseInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *dormantDatabaseInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&kubedb_v1alpha1.DormantDatabase{}, f.defaultInformer)
}

func (f *dormantDatabaseInformer) Lister() v1alpha1.DormantDatabaseLister {
	return v1alpha1.NewDormantDatabaseLister(f.Informer().GetIndexer())
}
