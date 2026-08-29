// Copyright 2026
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
)

// heldService describes a service declared on a MultiClusterService or a
// ClusterDeployment whose desired ServiceTemplate has not been propagated into an
// owned ServiceSet yet.
type heldService struct {
	key client.ObjectKey
	// want is the template the owning object asks for.
	want string
	// got is the template currently in the ServiceSet spec, empty when the service
	// is not present in the spec at all (never added, or dropped while locked).
	got string
	// clusters counts the target clusters holding this service back.
	clusters int
}

// maxHeldServicesListed bounds how many held services the ServiceDependencyReady message
// names individually. The full set is recoverable by diffing the owning object's spec
// against the owned ServiceSets; the condition only has to make the hold discoverable.
const maxHeldServicesListed = 3

// maxHeldServicesMessageBytes bounds the rendered message. Service, namespace and template
// names are each up to 253 bytes, so even a handful of named entries can grow large, and
// this string is persisted on the owning object's status.
const maxHeldServicesMessageBytes = 1024

// setServiceDependencyReadyCondition updates the ServiceDependencyReady condition, which
// reports whether every service declared on the owning object has reached the ServiceSets
// it owns.
//
// serviceset.FilterServiceDependencies deliberately holds a service back while a service it
// dependsOn is not yet deployed at its own desired version, carrying the previously deployed
// template over into the new ServiceSet spec. That is correct during a rollout, but until this
// condition existed it was entirely unobservable: the owned ServiceSet keeps reporting Deployed
// because the held services really are deployed - just at their old templates - so the
// services-ready conditions stay satisfied and the owning object kept reporting Ready=True at
// the current observedGeneration while quietly running a spec the user replaced. A hold that
// never resolves then looks identical to a converged deployment (KSM-207). Reporting it as a
// condition also flips Ready to False for as long as it lasts, via UpdateReadyCondition.
//
// Note this is scoped to propagation into existing ServiceSets. A target cluster with no
// ServiceSet at all is covered by the caller's own readiness condition and, on the
// MultiClusterService path, by MultiClusterServiceDependencyReady when an upstream
// MultiClusterService is what blocks its creation.
func setServiceDependencyReadyCondition(
	conditions *[]metav1.Condition,
	generation int64,
	desiredServices []kcmv1.Service,
	serviceSets []kcmv1.ServiceSet,
) {
	c := metav1.Condition{
		Type:               kcmv1.ServiceDependencyReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.SucceededReason,
		ObservedGeneration: generation,
	}

	if held, desired := heldServices(desiredServices, serviceSets); len(held) > 0 {
		c.Status = metav1.ConditionFalse
		c.Reason = kcmv1.ServiceDependencyNotReadyReason
		c.Message = heldServicesMessage(held, desired)
	}

	apimeta.SetStatusCondition(conditions, c)
}

// heldServices returns the services whose desired template has not reached at least one of the
// owned ServiceSets, in declaration order, along with the number of services that were expected
// to propagate at all.
//
// Services marked Disable are excluded: serviceset.ServicesToDeploy never emits them, so their
// absence from the ServiceSet spec is intentional rather than a hold.
func heldServices(desiredServices []kcmv1.Service, serviceSets []kcmv1.ServiceSet) (_ []heldService, desired int) {
	wanted := make(map[client.ObjectKey]string, len(desiredServices))
	order := make([]client.ObjectKey, 0, len(desiredServices))
	for _, svc := range desiredServices {
		if svc.Disable {
			continue
		}
		key := serviceset.ServiceKey(svc.Namespace, svc.Name)
		if _, seen := wanted[key]; !seen {
			order = append(order, key)
		}
		wanted[key] = svc.Template
	}
	if len(wanted) == 0 {
		return nil, 0
	}

	held := make(map[client.ObjectKey]*heldService, len(wanted))
	for _, ss := range serviceSets {
		// A ServiceSet being deleted is on its way out; its spec is not a propagation target.
		if !ss.DeletionTimestamp.IsZero() {
			continue
		}

		deployed := make(map[client.ObjectKey]string, len(ss.Spec.Services))
		for _, svc := range ss.Spec.Services {
			deployed[serviceset.ServiceKey(svc.Namespace, svc.Name)] = svc.Template
		}

		for key, want := range wanted {
			got, present := deployed[key]
			if present && got == want {
				continue
			}
			h, ok := held[key]
			if !ok {
				h = &heldService{key: key, want: want, got: got}
				held[key] = h
			}
			h.clusters++
		}
	}
	if len(held) == 0 {
		return nil, len(wanted)
	}

	result := make([]heldService, 0, len(held))
	for _, key := range order {
		if h, ok := held[key]; ok {
			result = append(result, *h)
		}
	}
	return result, len(wanted)
}

// heldServicesMessage renders held into a message bounded to maxHeldServicesMessageBytes.
func heldServicesMessage(held []heldService, desired int) string {
	listed := min(len(held), maxHeldServicesListed)
	parts := make([]string, 0, listed)
	for _, h := range held[:listed] {
		got := h.got
		if got == "" {
			got = "<not present>"
		}
		part := fmt.Sprintf("%s/%s has %s, wants %s", h.key.Namespace, h.key.Name, got, h.want)
		if h.clusters > 1 {
			part += fmt.Sprintf(" (on %d clusters)", h.clusters)
		}
		parts = append(parts, part)
	}

	msg := fmt.Sprintf("%d of %d service(s) not yet propagated to the owned ServiceSet(s), waiting on service dependencies: %s",
		len(held), desired, strings.Join(parts, "; "))
	if len(held) > listed {
		msg += fmt.Sprintf("; and %d more", len(held)-listed)
	}
	if len(msg) <= maxHeldServicesMessageBytes {
		return msg
	}

	omittedBytes := len(msg) - maxHeldServicesMessageBytes
	truncated := strings.ToValidUTF8(msg[:maxHeldServicesMessageBytes], "")
	return fmt.Sprintf("%s... (%d bytes omitted, diff the spec against the owned ServiceSet(s) for the full list)",
		truncated, omittedBytes)
}
