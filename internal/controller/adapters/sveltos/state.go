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

package sveltos

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/serviceset"
	"github.com/go-logr/logr"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This is taken from kubernetes defined at:
// https://github.com/kubernetes/kubernetes/blob/ce14ead/staging/src/k8s.io/apimachinery/pkg/util/validation/validation.go#L222
const dns1035LabelFmt string = "[a-z]([-a-z0-9]*[a-z0-9])?"

var releaseNameRgx *regexp.Regexp
var releaseNamespaceRgx *regexp.Regexp

func init() {
	releaseNameRgx = regexp.MustCompile("releaseName=(" + dns1035LabelFmt + ")")
	releaseNamespaceRgx = regexp.MustCompile("releaseNamespace=(" + dns1035LabelFmt + ")")
}

func servicesStateFromSummary(
	logger logr.Logger,
	summary *addoncontrollerv1beta1.ClusterSummary,
	serviceSet *kcmv1.ServiceSet,
) []kcmv1.ServiceState {
	if summary == nil {
		logger.Info("Could not collect services state because the provided ClusterSummary is nil")
		return nil
	}
	if serviceSet == nil {
		logger.Info("Could not collect services state because the provided ServiceSet is nil")
		return nil
	}

	logger.Info("Collecting services state from ClusterSummary", "cluster_summary", client.ObjectKeyFromObject(summary), "service_set", client.ObjectKeyFromObject(serviceSet))

	// We'll recreate service states list according to the desired services.
	states := make([]kcmv1.ServiceState, 0, len(serviceSet.Spec.Services))
	servicesMap := make(map[client.ObjectKey]kcmv1.ServiceState)

	for _, svc := range serviceSet.Spec.Services {
		servicesMap[serviceset.ServiceKey(svc.Namespace, svc.Name)] = kcmv1.ServiceState{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Template:  svc.Template,
			Version:   svc.Version,
			State:     kcmv1.ServiceStateProvisioning,
		}
	}

	/*
		NOTE: The reason why we can safely iterate over serviceSet's status here is because elsewhere
		in the code (in getHelmCharts, getKustomizationRefs & getPolicyRefs funcs) we modify the
		serviceSet object to add an entry (with default values) in its status for any service which
		exists in its spec but not in its status. This means that we can be sure that the serviceSet's
		status will already contain an entry for each of the services in the serviceSet's spec.
	*/
	for _, svc := range serviceSet.Status.Services {
		// We won't save state if the service is absent the spec.
		newState, ok := servicesMap[serviceset.ServiceKey(svc.Namespace, svc.Name)]
		if !ok {
			continue
		}

		newState.Type = svc.Type
		newState.LastStateTransitionTime = svc.LastStateTransitionTime

		switch svc.Type {
		case kcmv1.ServiceTypeHelm:
			featureHelm(&newState, summary)
		case kcmv1.ServiceTypeKustomize:
			featureKustomize(&newState, summary)
		case kcmv1.ServiceTypeResource:
			featureResources(&newState, summary)
		}

		if newState.State != svc.State {
			now := metav1.Now()
			newState.LastStateTransitionTime = &now
		}
		states = append(states, newState)
	}

	slices.SortFunc(states, func(a, b kcmv1.ServiceState) int {
		aKey := serviceset.ServiceKey(a.Namespace, a.Name)
		bKey := serviceset.ServiceKey(b.Namespace, b.Name)

		if n := cmp.Compare(aKey.Namespace, bKey.Namespace); n != 0 {
			return n
		}
		return cmp.Compare(aKey.Name, bKey.Name)
	})

	logger.V(1).Info("Collected services state from summary", "states", states)

	return states
}

func featureKustomize(newState *kcmv1.ServiceState, summary *addoncontrollerv1beta1.ClusterSummary) {
	hasKustomizations := len(summary.Spec.ClusterProfileSpec.KustomizationRefs) > 0

	kustomizationsDeployed := !hasKustomizations // Treat as deployed if feature absent.
	kustomizationsFailed := false
	kustomizationsFailureMessage := ""

	for _, feature := range summary.Status.FeatureSummaries {
		if feature.FeatureID == libsveltosv1beta1.FeatureKustomize {
			// We cannot determine which kustomizations or policies were failed, hence we'll treat them as failed
			// in case feature summary contains failure message. This message will be copied to the ServiceSet status
			// thus user will be able to see the reason of failure.
			// this is a temporary solution, we'll work with projectsveltos maintainers to improve observability.
			kustomizationsDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				kustomizationsFailed = true
				kustomizationsFailureMessage = *feature.FailureMessage
			}
			break
		}
	}

	if kustomizationsDeployed {
		newState.State = kcmv1.ServiceStateDeployed
	}
	if kustomizationsFailed {
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = "One or more Kustomizations failed to deploy: " + kustomizationsFailureMessage
	}
}

func featureResources(newState *kcmv1.ServiceState, summary *addoncontrollerv1beta1.ClusterSummary) {
	hasPolicies := len(summary.Spec.ClusterProfileSpec.PolicyRefs) > 0

	policiesDeployed := !hasPolicies // Treat as deployed if feature absent.
	policiesFailed := false
	policiesFailureMessage := ""

	for _, feature := range summary.Status.FeatureSummaries {
		if feature.FeatureID == libsveltosv1beta1.FeatureResources {
			policiesDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				policiesFailed = true
				policiesFailureMessage = *feature.FailureMessage
			}
			break
		}
	}

	// wahab: What if the feature is Provisioned but still carries an error msg?
	// If that's possible then we will mark it as Failed even if it has succeeded.
	// In any case maybe using else if would be safer. Same applied to Kustomizations.
	if policiesDeployed {
		newState.State = kcmv1.ServiceStateDeployed
	}
	if policiesFailed {
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = "One or more Resources failed to deploy: " + policiesFailureMessage
	}
}

func featureHelm(
	newState *kcmv1.ServiceState,
	summary *addoncontrollerv1beta1.ClusterSummary,
) {
	var helmFeatureFailMsg string
	var helmFeatureStatus libsveltosv1beta1.FeatureStatus

	for _, feature := range summary.Status.FeatureSummaries {
		if feature.FeatureID == libsveltosv1beta1.FeatureHelm {
			helmFeatureStatus = feature.Status
			// NOTE: The FailureMessage for the Helm Feature is simply a concatenation of
			// each individual `helmReleaseSummaries[].FailureMessage` since Sveltos v1.7.0.
			if feature.FailureMessage != nil {
				helmFeatureFailMsg = *feature.FailureMessage
			}
			break
		}
	}

	// Sometimes the failure message associated with a service is kept in the
	// Helm Feature's failure message even when it is removed from the service's
	// `helmReleaseSummaries[].FailureMessage`. For example when a service transitions
	// from Failed->Provisioning. So we parse the Helm Feature's failure message to
	// check if a failure message associated with a particular service is present or not.
	// This helps us in figuring out the proper states for each service in some scenarios.
	svcInHelmFeatureFailMsg := unpackHelmFailureMsg(helmFeatureFailMsg)

	// Create a helm release map from sveltos clustersummary for quicker lookup.
	helmReleaseMap := make(map[client.ObjectKey]*addoncontrollerv1beta1.HelmChartSummary)
	for _, helmRelease := range summary.Status.HelmReleaseSummaries {
		helmReleaseMap[client.ObjectKey{
			Namespace: helmRelease.ReleaseNamespace,
			Name:      helmRelease.ReleaseName,
		}] = &helmRelease
	}

	helmRelease := helmReleaseMap[serviceset.ServiceKey(newState.Namespace, newState.Name)]
	if helmRelease == nil {
		// Setting as not deployed because the service exists in
		// ServiceSet spec but not in ClusterSummary status yet.
		newState.State = kcmv1.ServiceStateNotDeployed
		return
	}

	convertedHelmFeatureStatus := featureStatusToServiceState(helmFeatureStatus)
	if convertedHelmFeatureStatus == kcmv1.ServiceStateDeleting ||
		convertedHelmFeatureStatus == kcmv1.ServiceStateDeleted {
		newState.State = convertedHelmFeatureStatus
		return
	}

	if helmRelease.Status == addoncontrollerv1beta1.HelmChartStatusConflict {
		newState.State = kcmv1.ServiceStateFailed
		// We can ignore `helmRelease.FailureMessage` when there's a conflict because
		// the ConflictMessage and FailureMessage cannot be both set at the same time.
		// github.com/projectsveltos/addon-controller/blob/f9b3752/controllers/handlers_helm.go#L2662-L2684
		newState.FailureMessage = helmRelease.ConflictMessage
		return
	}

	// Set the service's state to the status of the entire Helm Feature's status as a fallback.
	newState.State = convertedHelmFeatureStatus

	isValuesHashSet := len(helmRelease.ValuesHash) > 0
	isFailureMsgSet := helmRelease.FailureMessage != nil && *helmRelease.FailureMessage != ""

	switch {
	case !isValuesHashSet && !isFailureMsgSet:
		if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioning {
			/*
				The service could still be Provisioning like in the following example.

					featureSummaries:
					- featureID: Helm
						hash: 16CwFOvGTz25T2tUxoruIMDZaqv+AswuQylmSX/wKB8=
						status: Provisioning
					helmReleaseSummaries:
					- releaseName: nginx
						releaseNamespace: nginx
						status: Managing
					- releaseName: postgres-operator
						releaseNamespace: postgres-operator
						status: Managing
			*/
			newState.State = kcmv1.ServiceStateProvisioning
		} else {
			/*
				Or it could be Pending as below where postgres-operator isn't even attempted
				because continueOnError was False. One way we know that it is not even
				attempted is because the Helm Feature has a non-transitory (Failed) status
				but the postgres-operator has neither valuesHash nor failureMessage set.

					featureSummaries:
					- consecutiveFailures: 3
						failureMessage: 'chart: ingress-nginx, releaseNamespace: nginx, release: nginx,
							context deadline exceeded'
						featureID: Helm
						hash: p+CtqayLCV/oE2GmSQVgH/njs+PNS9jPuxK2fBfAA+A=
						lastAppliedTime: "2026-04-22T21:40:36Z"
						status: Failed
					helmReleaseSummaries:
					- failureMessage: context deadline exceeded
						releaseName: nginx
						releaseNamespace: nginx
						status: Managing
					- releaseName: postgres-operator
						releaseNamespace: postgres-operator
						status: Managing
			*/
			newState.State = kcmv1.ServiceStateNotDeployed
		}
	case !isValuesHashSet && isFailureMsgSet:
		// The service has failed to deploy.
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = *helmRelease.FailureMessage
	case isValuesHashSet && !isFailureMsgSet:
		if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioning {
			/*
					Here the service could either be Provisioning or Deployed and in some cases with
					multiple services we have no way of knowing which. Like in the example below:

						featureSummaries:
						- failureMessage: |
								chart: ingress-nginx, releaseNamespace: nginx, release: nginx, context deadline exceeded
							featureID: Helm
							hash: 16CwFOvGTz25T2tUxoruIMDZaqv+AswuQylmSX/wKB8=
							status: Provisioning
						helmReleaseSummaries:
						- releaseName: nginx
							releaseNamespace: nginx
							status: Managing
							valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
						- releaseName: postgres-operator
							releaseNamespace: postgres-operator
							status: Managing
							valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=

					1) nginx is (Deployed->Provisioning->Failed->Provisioning).
					   Starting with Deployed because it's valuesHash is set and then
						 moving on to Failed because the Helm Feature has a failure message for nginx.
						 Then finally moving on to Provisioning as the current state because it's own
						 failureMessage is not set, which means the Helm Feature's failureMessage is
						 from a previous Failed state.

					2) postgres-operator can either be (Deployed) or (Deployed->Provisioning).

					We can't know for sure whether postgres-operator is Deployed or Provisioning.
					So the best we can do is set the state to Provisioning if the Helm Feature's
					FailureMessage doesn't have an error belonging to the service. If it does then
					set the state to the status of the entire Helm Feature as a fallback (done earlier)
				  by doing nothing. These states would just be transitory because once the entire
					Helm Feature's status moves on from Provisioning to some other non-transitory value
					then we will be able to determine the actual state of the services.
			*/
			if _, ok := svcInHelmFeatureFailMsg[serviceset.ServiceKey(newState.Namespace, newState.Name)]; !ok {
				newState.State = kcmv1.ServiceStateProvisioning
			}
		}

		if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioned {
			// If the entire Helm Feature has been Provisioned then so has this service.
			newState.State = kcmv1.ServiceStateDeployed
		}

		if helmFeatureStatus == libsveltosv1beta1.FeatureStatusFailed {
			/*
				In this case it could be that one service has Failed as nginx has in the example
				below while the other service (postgres-operator) has successfully Deployed but
				since one of the services failed the overall status of Helm Feature reports Failed.

					featureSummaries:
					- failureMessage: |
							chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
						featureID: Helm
						hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
						status: Failed
					helmReleaseSummaries:
					- failureMessage: context deadline exceeded
						releaseName: nginx
						releaseNamespace: nginx
						status: Managing
					- releaseName: postgres-operator
						releaseNamespace: postgres-operator
						status: Managing
						valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=

				In such a case, since the Helm Feature's status is a non-transitory one (Failed) yet
				the postgres-operator service has an empty failureMessage and it's valuesHash set,
				we can be sure that it has been successfully Deployed.
			*/
			newState.State = kcmv1.ServiceStateDeployed
		}

		if helmFeatureStatus == libsveltosv1beta1.FeatureStatusFailedNonRetriable {
			/*
				This usually happens when a service has conflict, or if there are multiple
				services then at least one has a conflict while the others have been Deployed.
				If at least one has a conflict while another service has Failed then the overall
				status of the Helm Feature would be Failed instead of FailedNonRetriable.
			*/
			newState.State = kcmv1.ServiceStateDeployed
		}
	case isValuesHashSet && isFailureMsgSet:
		// This just means that the service is currently failing but a
		// previous version of the service was successfully deployed.
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = *helmRelease.FailureMessage
	}
}

func featureStatusToServiceState(featureStatus libsveltosv1beta1.FeatureStatus) string {
	state := kcmv1.ServiceStateNotDeployed

	switch featureStatus {
	case libsveltosv1beta1.FeatureStatusProvisioning:
		state = kcmv1.ServiceStateProvisioning
	case libsveltosv1beta1.FeatureStatusProvisioned:
		state = kcmv1.ServiceStateDeployed
	case libsveltosv1beta1.FeatureStatusFailed:
		state = kcmv1.ServiceStateFailed
	case libsveltosv1beta1.FeatureStatusFailedNonRetriable:
		// If the feature's status is FeatureStatusFailedNonRetriable this does not
		// mean complete failure. For example in then Helm Feature it could happen
		// that one service is Deployed successfully while the other has Conflict.
		// In such a case, the Helm Feature's status will be FeatureStatusFailedNonRetriable.
		state = kcmv1.ServiceStateFailed
	case libsveltosv1beta1.FeatureStatusRemoving:
		state = kcmv1.ServiceStateDeleting
	case libsveltosv1beta1.FeatureStatusAgentRemoving:
		state = kcmv1.ServiceStateDeleting
	case libsveltosv1beta1.FeatureStatusRemoved:
		state = kcmv1.ServiceStateDeleted
	}

	return state
}

// unpackHelmFailureMsg parses provided Helm Feature's failure message
// and returns a map of service keys that are contained in the message.
func unpackHelmFailureMsg(msg string) map[client.ObjectKey]struct{} {
	result := make(map[client.ObjectKey]struct{})

	for line := range strings.SplitSeq(msg, "\n") {
		releaseName := releaseNameRgx.FindString(line)
		releaseName, found := strings.CutPrefix(releaseName, "releaseName=")
		if !found {
			continue
		}

		releaseNamespace := releaseNamespaceRgx.FindString(line)
		releaseNamespace, found = strings.CutPrefix(releaseNamespace, "releaseNamespace=")
		if !found {
			continue
		}

		result[serviceset.ServiceKey(releaseNamespace, releaseName)] = struct{}{}
	}

	return result
}
