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

package exec //nolint:revive // it is okay for now

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
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
