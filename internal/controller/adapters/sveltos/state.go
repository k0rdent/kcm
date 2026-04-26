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
	"regexp"
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
	logger.Info("Collecting services state from ClusterSummary", "cluster_summary", client.ObjectKeyFromObject(summary))

	// We'll recreate service states list according to the desired services.
	states := make([]kcmv1.ServiceState, 0, len(serviceSet.Spec.Services))
	servicesMap := make(map[client.ObjectKey]kcmv1.ServiceState)

	for _, service := range serviceSet.Spec.Services {
		servicesMap[client.ObjectKey{
			Namespace: service.Namespace,
			Name:      service.Name,
		}] = kcmv1.ServiceState{
			Type:                    "",
			LastStateTransitionTime: nil,
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			Version:                 service.Version,
			State:                   kcmv1.ServiceStateProvisioning, // wahab: probably should being in Pending state?
			FailureMessage:          "",
		}
	}

	// NOTE: The reason why we can safely iterate over serviceSet's status here is because elsewhere
	// in the code (in getHelmCharts, getKustomizationRefs & getPolicyRefs funcs) we modify the
	// serviceSet object to add an entry (with default values) in its status for any service which
	// exists in its spec but not in its status. This means that we can be sure that the serviceSet's
	// status will already contain an entry for each of the services in the serviceSet's spec.
	for _, svc := range serviceSet.Status.Services {
		// we won't save state if the service is absent the spec.
		newState, ok := servicesMap[client.ObjectKey{
			Namespace: svc.Namespace,
			Name:      svc.Name,
		}]
		if !ok {
			continue
		}

		newState.Type = svc.Type
		newState.LastStateTransitionTime = svc.LastStateTransitionTime

		switch svc.Type {
		case kcmv1.ServiceTypeHelm:
			featureHelm(&svc, &newState, summary)
		case kcmv1.ServiceTypeKustomize:
			featureKustomize(&newState, summary)
		case kcmv1.ServiceTypeResource:
			featureResources(&newState, summary)
		}

		if newState.State != svc.State {
			newState.LastStateTransitionTime = new(metav1.Now())
		}
		states = append(states, newState)
	}
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
			// we cannot determine which kustomizations or policies were failed, hence we'll treat them as failed
			// in case feature summary contains failure message. This message will be copied to the ServiceSet status
			// thus user will be able to see the reason of failure.
			// this is a temporary solution, we'll work with projectsveltos maintainers to improve observability.
			kustomizationsDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				kustomizationsFailed = true
				kustomizationsFailureMessage = *feature.FailureMessage
			}
		}
	}

	if kustomizationsDeployed {
		newState.State = kcmv1.ServiceStateDeployed
	}
	if kustomizationsFailed {
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = "One or more Kustomizations failed to deploy:" + kustomizationsFailureMessage
	}
}

func featureResources(newState *kcmv1.ServiceState, summary *addoncontrollerv1beta1.ClusterSummary) {
	hasPolicies := len(summary.Spec.ClusterProfileSpec.PolicyRefs) > 0

	policiesDeployed := !hasPolicies // Treat as deployed if feature absent.
	policiesFailed := false
	policiesFailureMessage := ""

	for _, feature := range summary.Status.FeatureSummaries {
		switch feature.FeatureID {
		case libsveltosv1beta1.FeatureResources:
			policiesDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				policiesFailed = true
				policiesFailureMessage = *feature.FailureMessage
			}
		}
	}

	if policiesDeployed {
		newState.State = kcmv1.ServiceStateDeployed
	}
	if policiesFailed {
		newState.State = kcmv1.ServiceStateFailed
		newState.FailureMessage = "One or more Resources failed to deploy: " + policiesFailureMessage
	}
}

func featureHelm(
	currentState *kcmv1.ServiceState,
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
		}
	}

	// Create a helm release map from sveltos clustersummary for quicker lookup.
	helmReleaseMap := make(map[client.ObjectKey]*addoncontrollerv1beta1.HelmChartSummary)
	for _, helmRelease := range summary.Status.HelmReleaseSummaries {
		helmReleaseMap[client.ObjectKey{
			Namespace: helmRelease.ReleaseNamespace,
			Name:      helmRelease.ReleaseName,
		}] = &helmRelease
	}

	// Sometimes the failure message associated with a service is kept in the
	// Helm Feature's failure message even when it is removed from the service's
	// `helmReleaseSummaries[].FailureMessage`.For example when a service transitions
	// from Failed->Provisioning. So we parse the Helm Feature's failure message to
	// check if a failure message associated with a particular service is present or not.
	// This helps us in figuring out the proper states for each service in some scenarios.
	svcInHelmFeatureFailMsg := unpackHelmFailureMsg(helmFeatureFailMsg)

	if helmRelease := helmReleaseMap[serviceset.ServiceKey(currentState.Namespace, currentState.Name)]; helmRelease != nil {
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
		} else {
			// Set the entire Helm Feature's status as the service's state as fallback.
			newState.State = convertedHelmFeatureStatus

			isValuesHashSet := len(helmRelease.ValuesHash) > 0
			isFailureMsgSet := helmRelease.FailureMessage != nil && *helmRelease.FailureMessage != ""

			if !isValuesHashSet && !isFailureMsgSet {
				if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioning {
					/*
						The service could still be Provisioning like in the following example:

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
						Or it could be Pending as below where postgres-operator isn't even attempted because continueOnError was False:

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
			}

			if !isValuesHashSet && isFailureMsgSet {
				// The service has failed to deploy.
				newState.State = kcmv1.ServiceStateFailed
				newState.FailureMessage = *helmRelease.FailureMessage
			}

			if isValuesHashSet && !isFailureMsgSet {
				// wahab: We don't need to set state here as we handle Removing state separately
				//
				// Set service's state to whatever the Helm Feature's status is
				// unless it is Provisioning or Provisioned which we calculate below.
				// This covers states such as Removing as shown in the following example:
				//
				// featureSummaries:
				// - featureID: Helm
				//   status: Removing
				// - featureID: Resources
				//   status: Removing
				// - featureID: Kustomize
				//   status: Removing
				// helmReleaseSummaries:
				// - releaseName: nginx
				//   releaseNamespace: nginx
				//   status: Managing
				//   valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
				// - releaseName: postgres-operator
				//   releaseNamespace: postgres-operator
				//   status: Managing
				//   valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
				// newState.State = convertedHelmFeatureStatus

				if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioned {
					// If the entire Helm Feature has been Provisioned so has this service.
					newState.State = kcmv1.ServiceStateDeployed
				}

				if helmFeatureStatus == libsveltosv1beta1.FeatureStatusProvisioning {
					/*
						Case 1:
						The service could be Deployed->Provisioning as both nginx & postgres-operator are in the example below:

							featureSummaries:
							- featureID: Helm
								hash: LCMPOviWsexq8C1TgTZaiS7CPeCkqeEQ4Noex5iTqIQ=
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

						Case 2:
						Or the service could be Deployed->Failed->Provisioning as nginx is in the example below:

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

						In this case both have valuesHash set and empty failureMessage, so the only way to know that
						nginx is the one actually being Provisioned is to see if the Helm Feature's FailureMessage has a message
						for nginx from a previous version. Since this failure message has an entry only for nginx, we can be sure
						that postgres-operator is not Provisioning and is actually Deployed (actually it can be both Provisioning or Deployed) because it's valuesHash is already set.

						Case 3:
						But there is one more example as well where both services are Deployed -> Provisioning as shown below:

							featureSummaries:
							- featureID: Helm
								hash: LCMPOviWsexq8C1TgTZaiS7CPeCkqeEQ4Noex5iTqIQ=
								lastAppliedTime: "2026-04-22T21:53:28Z"
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

						We can differentiate this from Case 3 by checking if the Helm Feature's failure message is empty or not.
						If it is empty then the service is actually Provisioning.

						Case 4:
						The service could be Failed -> Provisioning as below:

							featureSummaries:
							- failureMessage: |
									chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
								featureID: Helm
								hash: 9uIsgqogeawbyindlsMevgCxjOoYRtTurwUU1aVDeBA=
								lastAppliedTime: "2026-04-23T08:28:19Z"
								status: Provisioning
							helmReleaseSummaries:
							- releaseName: nginx
								releaseNamespace: nginx
								status: Managing
								valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=

						Since nginx is the only service it is obviously Provisioning, this case
						is handled by setting the service's state to the Helm Feature's status as
						done in the before these if statements.
					*/
					if helmFeatureFailMsg == "" {
						newState.State = kcmv1.ServiceStateProvisioning
					} else {
						// Here the service could either be Provisioning or Deployed and we have no way
						// of knowing which so we will set it as Provisioning in this transitory period.
						// Once the entire Helm Feature's status moves on from Provisioning to some other
						// value, we will be able to determine the actual state of the service then.
						if _, ok := svcInHelmFeatureFailMsg[serviceset.ServiceKey(currentState.Namespace, currentState.Name)]; !ok {
							// newState.State = kcmv1.ServiceStateDeployed
							newState.State = kcmv1.ServiceStateProvisioning
						}
					}
				}

				if helmFeatureStatus == libsveltosv1beta1.FeatureStatusFailed {
					// In this case it could be that one service has Failed as nginx in the example blow
					// while the other service (postgres-operator) has successfully deployed but
					// since one of the services failed the overall status of Helm Feature reports Failed.
					//
					// featureSummaries:
					// - failureMessage: |
					//     chart=ingress-nginx, releaseNamespace=nginx, releaseName=nginx: context deadline exceeded
					//   featureID: Helm
					//   hash: 9J6imd1Rsr9faFztMeQDOQthMKWpFk2FyQ0WBm89XNg=
					//   status: Failed
					// helmReleaseSummaries:
					// - failureMessage: context deadline exceeded
					//   releaseName: nginx
					//   releaseNamespace: nginx
					//   status: Managing
					// - releaseName: postgres-operator
					//   releaseNamespace: postgres-operator
					//   status: Managing
					//   valuesHash: yj0WO6sFU4GCciYUBWjzvvfqrBh869doeOC2Pp5EI1Y=
					//
					// So in such a case, we will again parse the Helm Feature's FailureMessage has
					// a failure message for the service. If not then the service has been Deployed.
					if _, ok := svcInHelmFeatureFailMsg[serviceset.ServiceKey(currentState.Namespace, currentState.Name)]; !ok {
						newState.State = kcmv1.ServiceStateDeployed
					}
				}

				if helmFeatureStatus == libsveltosv1beta1.FeatureStatusFailedNonRetriable {
					// This status usually occurs when one service has conflict and the other is Deployed.
					newState.State = kcmv1.ServiceStateDeployed
				}
			}

			if isValuesHashSet && isFailureMsgSet {
				// This just means that the release is currently failing but some
				// previous version of the release was successfully deployed.
				newState.State = kcmv1.ServiceStateFailed
				newState.FailureMessage = *helmRelease.FailureMessage
			}
		}
	} else {
		// Setting as not deployed because the service exists in
		// ServiceSet spec but not in ClusterSummary status yet.
		newState.State = kcmv1.ServiceStateNotDeployed
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
		// If the feature's status is FeatureStatusFailedNonRetriable this doesn't necessarily
		// mean complete failure. For example in Helm Feature it could happen that one service
		// is Deployed successfully while the other has Conflict.
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
