// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package metadata

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/common/kubernetes"
)

type pod struct {
	store               cache.Store
	client              k8s.Interface
	node                MetaGen
	namespace           MetaGen
	resource            *Resource
	addResourceMetadata *AddResourceMetadataConfig
}

// NewPodMetadataGenerator creates a metagen for pod resources
func NewPodMetadataGenerator(
	cfg *common.Config,
	pods cache.Store,
	client k8s.Interface,
	node MetaGen,
	namespace MetaGen,
	addResourceMetadata *AddResourceMetadataConfig) MetaGen {

	if addResourceMetadata == nil {
		addResourceMetadata = GetDefaultResourceMetadataConfig()
	}

	return &pod{
		resource:            NewResourceMetadataGenerator(cfg, client),
		store:               pods,
		node:                node,
		namespace:           namespace,
		client:              client,
		addResourceMetadata: addResourceMetadata,
	}
}

// Generate generates pod metadata from a resource object
// Metadata map is in the following form:
// {
// 	  "kubernetes": {},
//    "some.ecs.field": "asdf"
// }
// All Kubernetes fields that need to be stored under kubernetes. prefix are populated by
// GenerateK8s method while fields that are part of ECS are generated by GenerateECS method
func (p *pod) Generate(obj kubernetes.Resource, opts ...FieldOptions) common.MapStr {
	ecsFields := p.GenerateECS(obj)
	meta := common.MapStr{
		"kubernetes": p.GenerateK8s(obj, opts...),
	}
	meta.DeepUpdate(ecsFields)
	return meta
}

// GenerateECS generates pod ECS metadata from a resource object
func (p *pod) GenerateECS(obj kubernetes.Resource) common.MapStr {
	return p.resource.GenerateECS(obj)
}

// GenerateK8s generates pod metadata from a resource object
func (p *pod) GenerateK8s(obj kubernetes.Resource, opts ...FieldOptions) common.MapStr {
	po, ok := obj.(*kubernetes.Pod)
	if !ok {
		return nil
	}

	out := p.resource.GenerateK8s("pod", obj, opts...)

	// check if Pod is handled by a ReplicaSet which is controlled by a Deployment
	if p.addResourceMetadata.Deployment {
		rsName, _ := out.GetValue("replicaset.name")
		if rsName, ok := rsName.(string); ok {
			dep := p.getRSDeployment(rsName, po.GetNamespace())
			if dep != "" {
				out.Put("deployment.name", dep)
			}
		}
	}

	if p.node != nil {
		meta := p.node.GenerateFromName(po.Spec.NodeName, WithLabels("node"))
		if meta != nil {
			out.Put("node", meta["node"])
		} else {
			out.Put("node.name", po.Spec.NodeName)
		}
	} else {
		out.Put("node.name", po.Spec.NodeName)
	}

	if p.namespace != nil {
		meta := p.namespace.GenerateFromName(po.GetNamespace())
		if meta != nil {
			out.DeepUpdate(meta)
		}
	}

	if po.Status.PodIP != "" {
		out.Put("pod.ip", po.Status.PodIP)
	}

	return out
}

// GenerateFromName generates pod metadata from a pod name
func (p *pod) GenerateFromName(name string, opts ...FieldOptions) common.MapStr {
	if p.store == nil {
		return nil
	}

	if obj, ok, _ := p.store.GetByKey(name); ok {
		po, ok := obj.(*kubernetes.Pod)
		if !ok {
			return nil
		}

		return p.GenerateK8s(po, opts...)
	}

	return nil
}

// getRSDeployment return the name of the Deployment object that
// owns the ReplicaSet with the given name under the given Namespace
func (p *pod) getRSDeployment(rsName string, ns string) string {
	if p.client == nil {
		return ""
	}
	rs, err := p.client.AppsV1().ReplicaSets(ns).Get(context.TODO(), rsName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	for _, ref := range rs.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller {
			switch ref.Kind {
			case "Deployment":
				return ref.Name
			}
		}
	}
	return ""
}
