// Copyright 2025
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

package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// CheckAllSecretsExistInNamespace returns positive if all of the Secrets with the given names
// exist in the given namespace.
// Empty names are ignored.
func CheckAllSecretsExistInNamespace(ctx context.Context, cl client.Client, namespace string, names ...string) (exist bool, missingSecrets []string, err error) {
	for _, name := range names {
		exists, err := checkSecretExistsInNamespace(ctx, namespace, cl, name)
		if err != nil {
			return false, nil, fmt.Errorf("failed to fetch Secret: %w", err)
		}
		if !exists {
			missingSecrets = append(missingSecrets, name)
		}
	}

	return len(missingSecrets) == 0, missingSecrets, nil
}

// checkSecretExistsInNamespace checks if a Secret with the given name exists in the given namespace.
// Empty name is ignored.
func checkSecretExistsInNamespace(ctx context.Context, namespace string, cl client.Client, name string) (bool, error) {
	if len(name) == 0 { // sanity check
		return true, nil
	}
	err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &corev1.Secret{})
	return err == nil, client.IgnoreNotFound(err)
}

// ObjectConditionGetter is a common interface over [sigs.k8s.io/controller-runtime/pkg/client.Object]
// that can get a reference to a slice of [k8s.io/apimachinery/pkg/apis/meta/v1.Condition] to manipulate
// the object directly.
type ObjectConditionGetter interface {
	client.Object
	GetConditions() *[]metav1.Condition
}

func getPredeclaredSecretsExistCondition(generation int64, failedMsg string) metav1.Condition {
	condition := metav1.Condition{
		Type:               kcmv1.PredeclaredSecretsExistCondition,
		ObservedGeneration: generation,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.SucceededReason,
		Message:            "All predeclared Secrets exist",
	}

	if failedMsg != "" {
		condition.Status = metav1.ConditionFalse
		condition.Reason = kcmv1.FailedReason
		condition.Message = failedMsg
	}

	return condition
}

// SetPredeclaredSecretsCondition checks if all Secret objects with the given names in the given namespace exist.
// If one of the Secret objects does not exist, creates a new warning Event and returns error.
// In either case, sets the [github.com/K0rdent/kcm/api/v1beta1.PredeclaredSecretsExistCondition] condition with corresponding status.
//
// Does nothing if all of the given names are empty.
func SetPredeclaredSecretsCondition(ctx context.Context, cl client.Client, base ObjectConditionGetter, eventFn func(runtime.Object, int64, string, string, ...any), namespace string, names ...string) (changed bool, err error) {
	if len(names) == 0 {
		return false, nil
	}

	fastReturn := true
	for _, v := range names {
		if v != "" {
			fastReturn = false
			break
		}
	}
	if fastReturn {
		return false, nil
	}

	exist, missingSecrets, err := CheckAllSecretsExistInNamespace(ctx, cl, namespace, names...)
	if err != nil {
		return false, fmt.Errorf("failed to check if Secrets %v exists: %w", names, err)
	}

	ldebug := ctrl.LoggerFrom(ctx).V(1).WithName("secrets-checker").WithValues("given_secrets", names, "given_namespace", namespace)
	if exist {
		ldebug.Info("All Secrets exist")
		return meta.SetStatusCondition(base.GetConditions(), getPredeclaredSecretsExistCondition(base.GetGeneration(), "")), nil
	}

	ldebug.Info("Some required Secrets are missing")

	missingMsg := fmt.Sprintf("Some of the predeclared Secrets (%v) are missing (%v) in the %s namespace", names, missingSecrets, namespace)

	if changed = meta.SetStatusCondition(base.GetConditions(), getPredeclaredSecretsExistCondition(base.GetGeneration(), missingMsg)); changed {
		eventFn(base, base.GetGeneration(), "MissingDeclaredSecrets", missingMsg)
	}

	return changed, fmt.Errorf("missing secret names: %v", missingSecrets)
}

// CopySecret copies a Secret with the given key to the given namespace.
func CopySecret(ctx context.Context, cl client.Client, key client.ObjectKey, toNamespace string) error {
	if key.Name == "" || key.Namespace == toNamespace { // sanity check
		return nil
	}

	secret := new(corev1.Secret)
	if err := cl.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("failed to get Secret %s: %w", key, err)
	}

	newSecret := secret.DeepCopy()
	newSecret.ObjectMeta = metav1.ObjectMeta{
		Name:      key.Name,
		Namespace: toNamespace,
	}

	if err := cl.Create(ctx, newSecret); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Secret %s/%s: %w", newSecret.Namespace, newSecret.Name, err)
	}

	return nil
}
