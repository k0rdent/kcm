// Copyright 2024
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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/K0rdent/kcm/internal/utils/status"
)

const (
	KCMControllerLabel = "app.kubernetes.io/name=kcm"
)

// Run executes the provided command within this context and returns it's
// output. Run does not wait for the command to finish, use Wait instead.
func Run(cmd *exec.Cmd) ([]byte, error) {
	command := prepareCmd(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %s\n", command)

	var outputBuffer bytes.Buffer

	multiWriter := io.MultiWriter(&outputBuffer, GinkgoWriter)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	err := cmd.Run()
	if err != nil {
		return nil, handleCmdError(err, command)
	}
	return outputBuffer.Bytes(), nil
}

func handleCmdError(err error, command string) error {
	var exitError *exec.ExitError

	if errors.As(err, &exitError) {
		return fmt.Errorf("%s failed with error: (%v): %s", command, err, string(exitError.Stderr))
	}

	return fmt.Errorf("%s failed with error: %w", command, err)
}

func prepareCmd(cmd *exec.Cmd) string {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %s\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	return strings.Join(cmd.Args, " ")
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, err
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

// ValidateConditionsTrue iterates over the conditions of the given
// unstructured object and returns an error if any of the conditions are not
// true.  Conditions are expected to be of type metav1.Condition.
func ValidateConditionsTrue(unstrObj *unstructured.Unstructured) error {
	objKind, objName := status.ObjKindName(unstrObj)

	conditions, err := status.ConditionsFromUnstructured(unstrObj)
	if err != nil {
		return fmt.Errorf("failed to get conditions from unstructured object: %w", err)
	}

	var errs error

	for _, c := range conditions {
		if c.Status == metav1.ConditionTrue {
			continue
		}

		errs = errors.Join(errors.New(ConvertConditionsToString(c)), errs)
	}

	if errs != nil {
		return fmt.Errorf("%s %s is not ready with conditions:\n%w", objKind, objName, errs)
	}

	return nil
}

func ConvertConditionsToString(condition metav1.Condition) string {
	return fmt.Sprintf("Type: %s, Status: %s, Reason: %s, Message: %s",
		condition.Type, condition.Status, condition.Reason, condition.Message)
}

// ValidateObjectNamePrefix checks if the given object name has the given prefix.
func ValidateObjectNamePrefix(obj *unstructured.Unstructured, prefix string) error {
	objKind, objName := status.ObjKindName(obj)

	// Verify the machines are prefixed with the cluster name and fail
	// the test if they are not.
	if !strings.HasPrefix(objName, prefix) {
		return fmt.Errorf("object %s %s does not have prefix: %s", objKind, objName, prefix)
	}

	return nil
}

func WarnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "Warning: %v\n", err)
}
