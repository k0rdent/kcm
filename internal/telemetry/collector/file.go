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
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// file encapsulates the current day's temp file, its date, and provides rotation/flush logic.
type file struct {
	f       *os.File // underlying temp file (telemetry-<day>.json.tmp)
	logger  logr.Logger
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

func (f *file) setLogger(l logr.Logger) {
	if f.logger.GetSink() == nil {
		f.logger = l
	}
}

// openTempLocked must be called under lock. It opens the temp file for f.today.
func (f *file) openTempLocked() error {
	var err error
	f.f, err = os.OpenFile(f.tempPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open temp file %s: %w", f.tempPath(), err)
	}

	f.logger.Info("opened a new file", "fname", f.f.Name())

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

	f.logger.Info("ensuring file is open")

	if err := f.rotateIfNeededLocked(); err != nil {
		return fmt.Errorf("failed to rotate file: %w", err)
	}

	if f.f == nil { // should not normally happen
		f.logger.Info("this should not have happened but still we ensure a new file")
		return f.openTempLocked()
	}

	if _, err := os.Stat(f.f.Name()); errors.Is(err, fs.ErrNotExist) {
		f.logger.Info("file was in memory but was not on the file system", "fname", f.f.Name())
		return f.openTempLocked()
	}

	return nil
}

// rotateIfNeededLocked performs rotation if the day has advanced. Caller must hold lock.
func (f *file) rotateIfNeededLocked() error {
	if isSameDay(time.Now(), f.today) {
		return nil // nothing to do
	}

	f.logger.Info("rotating file")
	if f.f != nil {
		f.logger.Info("closing file", "fname", f.f.Name())
		_ = f.f.Sync()
		_ = f.f.Close()
		f.f = nil
	}

	// best-effort rename
	tempPath, finalPath := f.tempPath(), f.permanentPath()
	if _, err := os.Stat(tempPath); err == nil {
		f.logger.Info("renaming file", "from", tempPath, "to", finalPath)
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
		f.logger.Info("nothing to flush")
		return f.rotateIfNeededLocked()
	}

	f.logger.Info("flushing data", "incoming entries", len(entries))
	if f.f == nil {
		f.logger.Info("open a new temp file since the old one is absent")
		if err := f.openTempLocked(); err != nil {
			return fmt.Errorf("failed to open temp file: %w", err)
		}
	}

	onDisk, err := f.readMergedOnDisk()
	if err != nil {
		return fmt.Errorf("failed to read state from disk: %w", err)
	}

	f.logger.Info("ondisk state before merging with the incoming entries", "on disk", onDisk)
	f.mergeMaps(onDisk, entries)
	state := mapToState(onDisk)
	f.logger.Info("merged and sorted state", "state", state)

	updatedContents, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	f.logger.Info("truncating, seek, write and sync", "fname", f.f.Name())
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

	f.logger.Info("cleaning the entries")
	for k := range entries {
		delete(entries, k)
	}

	f.logger.Info("rotating if required", "fname", f.f.Name())
	return f.rotateIfNeededLocked()
}

// closeAndRotate is called on graceful shutdown.
func (f *file) closeAndRotate() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.logger.Info("closing and rotating the temp file")

	if f.f != nil {
		if err := f.f.Sync(); err != nil {
			return fmt.Errorf("failed to sync %s: %w", f.f.Name(), err)
		}
		if err := f.f.Close(); err != nil {
			return fmt.Errorf("failed to close %s: %w", f.f.Name(), err)
		}
		f.f = nil
	}

	if isSameDay(time.Now(), f.today) {
		f.logger.Info("temp file is closed but preserved since the day has not flipped")
		return nil
	}

	tempPath, finalPath := f.tempPath(), f.permanentPath()
	if fi, err := os.Stat(tempPath); err == nil && fi.Size() > 0 {
		f.logger.Info("renaming file", "from", tempPath, "to", finalPath)
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

// stateToMap converts a fileState into a map keyed by ClusterDeployment name.
func stateToMap(st fileState) map[string]clusterEntry {
	m := make(map[string]clusterEntry, len(st.Clusters))
	for _, e := range st.Clusters {
		if c := e.Labels[labelCluster]; c != "" {
			e.ensure(c)
			m[c] = e
		}
	}
	return m
}

// mapToState converts a map back into a sorted fileState (by ClusterDeployment name).
func mapToState(clusterEntries map[string]clusterEntry) fileState {
	out := fileState{Clusters: make([]clusterEntry, 0, len(clusterEntries))}

	clusterNames := slices.Collect(maps.Keys(clusterEntries))
	sort.Strings(clusterNames)

	for _, clName := range clusterNames {
		out.Clusters = append(out.Clusters, clusterEntries[clName])
	}

	return out
}

func (f *file) mergeClusterEntry(dst, src clusterEntry) clusterEntry {
	dst.ensure(dst.Labels[labelCluster])
	src.ensure(src.Labels[labelCluster])

	maps.Copy(dst.Labels, src.Labels)

	for k, v := range src.Counters {
		n := dst.Counters[k] + v
		if n < dst.Counters[k] {
			f.logger.Error(errors.New("counter overflow"), "counter uin64 overflow", "counter", k, "counter value", v, "on disk value", dst.Counters[k])
			continue
		}
		dst.Counters[k] = n
	}

	return dst
}

func (f *file) mergeMaps(dstMap, srcMap map[string]clusterEntry) {
	for cldNamespacedName, src := range srcMap {
		if cldNamespacedName == "" {
			continue
		}

		dst := dstMap[cldNamespacedName]

		dstMap[cldNamespacedName] = f.mergeClusterEntry(dst, src)
	}
}

func (f *file) tryReadState(path string) (fileState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileState{}, nil
		}

		return fileState{}, fmt.Errorf("failed to read %s: %w", path, err)
	}

	if len(bytes.TrimSpace(b)) == 0 {
		return fileState{}, nil
	}

	var st fileState
	if err := json.Unmarshal(b, &st); err != nil {
		ts := time.Now().UTC().Format("20060102T150405Z")
		f.logger.Error(err, "failed to read current state, renaming", "corrupted", path)
		_ = os.Rename(path, path+".corrupt-"+ts)
		return fileState{}, nil
	}

	return st, nil
}

func (f *file) readMergedOnDisk() (map[string]clusterEntry, error) {
	final, err := f.tryReadState(f.permanentPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read state from the %s: %w", f.permanentPath(), err)
	}
	f.logger.Info("state from the perm file", "fname", f.permanentPath(), "state", final)

	tmp, err := f.tryReadState(f.tempPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read state from the %s: %w", f.tempPath(), err)
	}
	f.logger.Info("state from the temp file", "fname", f.tempPath(), "state", tmp)

	fm := stateToMap(final)
	f.mergeMaps(fm, stateToMap(tmp))
	return fm, nil
}
