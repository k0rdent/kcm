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

package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// file encapsulates the current day's temp file, its date, and provides rotation/flush logic.
type file struct {
	f       *os.File  // underlying temp file (telemetry-<day>.json.tmp)
	today   time.Time // midnight UTC of the current day this file represents
	baseDir string    // directory where files live
	mu      sync.Mutex
}

type fileState struct {
	Clusters []clusterEntry `json:"clusters,omitempty"`
}

// newFile initializes the file for today, creating the temp file if needed.
func newFile(baseDir string) (*file, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create dir %s: %w", baseDir, err)
	}
	f := &file{today: dateOnlyTime(time.Now()), baseDir: baseDir}
	if err := f.openTempLocked(); err != nil {
		return nil, fmt.Errorf("failed to open temp: %w", err)
	}
	return f, nil
}

// openTempLocked must be called under lock. It opens the temp file for f.today.
func (f *file) openTempLocked() error {
	var err error
	f.f, err = os.OpenFile(f.tempPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open temp file %s: %w", f.tempPath(), err)
	}

	return nil
}

func (f *file) tempPath() string {
	return filepath.Join(f.baseDir, fmt.Sprintf("telemetry-%s.json.tmp", f.today.Format(time.DateOnly)))
}

func (f *file) permanentPath() string {
	return filepath.Join(f.baseDir, f.today.Format(time.DateOnly)+".json")
}

// ensureOpen makes sure the temp for the current day is available.
// If a new day has started since the last caller, the previous temp is
// finalised and a fresh handle is opened.
func (f *file) ensureOpen() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.rotateIfNeededLocked(); err != nil {
		return fmt.Errorf("failed to rotate file: %w", err)
	}
	if f.f == nil { // should not normally happen
		return f.openTempLocked()
	}
	return nil
}

// rotateIfNeededLocked performs rotation if the day has advanced. Caller must hold lock.
func (f *file) rotateIfNeededLocked() error {
	if isSameDay(time.Now(), f.today) {
		return nil // nothing to do
	}

	if f.f != nil {
		_ = f.f.Sync()
		_ = f.f.Close()
		f.f = nil
	}

	// best-effort rename
	tempPath, finalPath := f.tempPath(), f.permanentPath()
	if _, err := os.Stat(tempPath); err == nil {
		_ = os.Rename(tempPath, finalPath)
	}
	f.today = dateOnlyTime(time.Now())
	return f.openTempLocked()
}

// flush merges `entries` into on‑disk state for the day this scrape started.
// It truncates‑in‑place, writes JSON, fsyncs. Once finished it
// rotates the file if day changed during the scrape.
// Cleans the `entries` upon fsync.
func (f *file) flush(entries map[string]clusterEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(entries) == 0 {
		return f.rotateIfNeededLocked()
	}

	if f.f == nil {
		if err := f.openTempLocked(); err != nil {
			return fmt.Errorf("failed to open temp file: %w", err)
		}
	}

	state, err := f.readState()
	if err != nil {
		return fmt.Errorf("failed to read state from the temp file %s: %w", f.f.Name(), err)
	}

	onDisk := make(map[string]clusterEntry, len(state.Clusters))
	for _, e := range state.Clusters {
		if c, ok := e.Labels[labelCluster]; ok && c != "" {
			onDisk[c] = e
		}
	}

	for cldNamespacedName, ce := range entries {
		if cldNamespacedName == "" {
			continue
		}

		e, ok := onDisk[cldNamespacedName]
		if !ok {
			e = clusterEntry{
				Labels:   map[string]string{labelCluster: cldNamespacedName},
				Counters: make(map[string]uint64),
			}
		}

		if e.Labels == nil {
			e.Labels = map[string]string{labelCluster: cldNamespacedName}
		}
		maps.Copy(e.Labels, ce.Labels)

		if e.Counters == nil {
			e.Counters = make(map[string]uint64)
		}
		for k, v := range ce.Counters {
			n := e.Counters[k] + v
			if n < e.Counters[k] { // overflow guard
				continue
			}
			e.Counters[k] = n
		}

		onDisk[cldNamespacedName] = e
	}

	// rebuild sorted state
	keys := slices.Collect(maps.Keys(onDisk))
	sort.Strings(keys)
	state.Clusters = state.Clusters[:0]
	for _, k := range keys {
		state.Clusters = append(state.Clusters, onDisk[k])
	}

	updatedContents, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := f.f.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate %s: %w", f.f.Name(), err)
	}
	if _, err := f.f.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek %s: %w", f.f.Name(), err)
	}
	if _, err := f.f.Write(updatedContents); err != nil {
		return fmt.Errorf("failed to write contents to %s: %w", f.f.Name(), err)
	}
	if err := f.f.Sync(); err != nil {
		return fmt.Errorf("failed to sync %s: %w", f.f.Name(), err)
	}

	for k := range entries {
		delete(entries, k)
	}

	return f.rotateIfNeededLocked()
}

// closeAndRotate is called on graceful shutdown.
func (f *file) closeAndRotate() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.f != nil {
		if err := f.f.Sync(); err != nil {
			return fmt.Errorf("failed to sync %s: %w", f.f.Name(), err)
		}
		if err := f.f.Close(); err != nil {
			return fmt.Errorf("failed to close %s: %w", f.f.Name(), err)
		}
		f.f = nil
	}

	tempPath, finalPath := f.tempPath(), f.permanentPath()
	if _, err := os.Stat(tempPath); err == nil {
		if err := os.Rename(tempPath, finalPath); err != nil {
			return fmt.Errorf("failed to rename file %s to %s: %w", tempPath, finalPath, err)
		}
	}
	return nil
}

func dateOnlyTime(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func isSameDay(now, day time.Time) bool {
	return dateOnlyTime(now).Equal(day)
}

// readState reads JSON from both temp and permanent paths respectively.
// The first existing, readable path, returns the state.
// If a file is present but corrupt, it is renamed with .corrupt-<timestamp> and an empty
// state is returned.
func (f *file) readState() (fileState, error) {
	paths := []string{f.tempPath(), f.permanentPath()}
	var st fileState
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return fileState{}, fmt.Errorf("failed to read %s: %w", p, err)
		}

		if len(bytes.TrimSpace(b)) == 0 {
			return st, nil
		}

		if err := json.Unmarshal(b, &st); err != nil {
			ts := time.Now().UTC().Format("20060102T150405Z")
			_ = os.Rename(p, p+".corrupt-"+ts)
			return fileState{}, nil //nolint:nilerr // skip err on purpose
		}

		return st, nil
	}

	return st, nil
}
