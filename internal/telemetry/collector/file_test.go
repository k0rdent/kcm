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
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func mustNewFile(t *testing.T) *file {
	t.Helper()

	reqs := require.New(t)
	dir := t.TempDir()

	f, err := newFile(dir)
	reqs.NoError(err)
	f.setLogger(logr.Discard())
	return f
}

func Test_isSameDay(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)

	now := time.Now()
	reqs.True(isSameDay(now, dateOnlyTime(now)), "same day should be true")
	reqs.False(isSameDay(now.Add(24*time.Hour), dateOnlyTime(now)), "next day should be false")
}

func Test_ensureOpen(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	_, err := os.Stat(f.tempPath())
	reqs.NoError(err, "temp should exist after newFile")

	reqs.NoError(os.Remove(f.tempPath()))
	reqs.NoError(f.ensureOpen())
	_, err = os.Stat(f.tempPath())
	reqs.NoError(err, "temp should be recreated by ensureOpen")

	reqs.NoError(f.ensureOpen(), "multiple calls should not error")
}

func Test_stateToMap_mapToState(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)

	st := fileState{
		Clusters: []clusterEntry{
			{Labels: map[string]string{labelCluster: "b"}, Counters: map[string]uint64{"x": 1}},
			{Labels: map[string]string{labelCluster: "a"}, Counters: map[string]uint64{"y": 2}},
		},
	}
	m := stateToMap(st)
	reqs.Len(m, 2)
	reqs.Equal(uint64(2), m["a"].Counters["y"])
	reqs.Equal(uint64(1), m["b"].Counters["x"])

	st2 := mapToState(m)
	// check sorted
	reqs.Len(st2.Clusters, 2)
	reqs.Equal("a", st2.Clusters[0].Labels[labelCluster])
	reqs.Equal("b", st2.Clusters[1].Labels[labelCluster])
}

func Test_mergeClusterEntry(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	const (
		someCluster = "ns/name"
		someLabel   = "someLabel"
	)

	dst := clusterEntry{
		Labels:   map[string]string{labelCluster: someCluster, "l1": "x"},
		Counters: map[string]uint64{"c1": 2},
	}
	src := clusterEntry{
		Labels:   map[string]string{labelCluster: someCluster, "l2": "y"},
		Counters: map[string]uint64{"c1": 3, "c2": 1},
	}
	merged := f.mergeClusterEntry(dst, src)
	reqs.Equal(uint64(5), merged.Counters["c1"])
	reqs.Equal(uint64(1), merged.Counters["c2"])
	reqs.Equal("x", merged.Labels["l1"])
	reqs.Equal("y", merged.Labels["l2"])

	// check overflow
	dst = clusterEntry{
		Labels:   map[string]string{labelCluster: someCluster},
		Counters: map[string]uint64{someLabel: ^uint64(0) - 1},
	}
	src = clusterEntry{
		Labels:   map[string]string{labelCluster: someCluster},
		Counters: map[string]uint64{someLabel: 10},
	}
	merged = f.mergeClusterEntry(dst, src)
	reqs.Equal(^uint64(0)-1, merged.Counters[someLabel])
}

func Test_mergeMaps(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	const (
		someCluster      = "ns/name"
		someOtherCluster = "ns/other-name"
		someLabel        = "someLabel"
	)

	dst := map[string]clusterEntry{
		someCluster: {Labels: map[string]string{labelCluster: someCluster}, Counters: map[string]uint64{"a": 1}},
	}
	src := map[string]clusterEntry{
		"":               {Labels: map[string]string{}, Counters: map[string]uint64{"skip": 1}},
		someCluster:      {Labels: map[string]string{labelCluster: someCluster}, Counters: map[string]uint64{"a": 2}},
		someOtherCluster: {Labels: map[string]string{labelCluster: someOtherCluster}, Counters: map[string]uint64{"b": 5}},
	}

	f.mergeMaps(dst, src)
	reqs.Equal(uint64(3), dst[someCluster].Counters["a"])
	reqs.Equal(uint64(5), dst[someOtherCluster].Counters["b"])
	reqs.Empty(dst[""], "empty key should not be merged in")
}

func Test_tryReadState(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	// non-existent
	st, err := f.tryReadState(filepath.Join(f.baseDir, "does-not-exist.json"))
	reqs.NoError(err)
	reqs.Empty(st.Clusters)

	// empty file
	empty := filepath.Join(f.baseDir, "empty.json")
	reqs.NoError(os.WriteFile(empty, []byte(" \n"), 0o644))
	st, err = f.tryReadState(empty)
	reqs.NoError(err)
	reqs.Empty(st.Clusters)

	// corrupt
	cor := filepath.Join(f.baseDir, "corrupt.json")
	reqs.NoError(os.WriteFile(cor, []byte("{not-json"), 0o644))
	st, err = f.tryReadState(cor)
	reqs.NoError(err)
	reqs.Empty(st.Clusters)

	// ensure renamed to .corrupt-<timestamp>
	matches, _ := filepath.Glob(cor + ".corrupt-*")
	reqs.NotEmpty(matches, "corrupt file should be renamed with .corrupt-<ts> suffix")
}

func Test_readMergedOnDisk(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	const someCluster = "ns/name"

	// permanent has one, temp has another
	perm := fileState{Clusters: []clusterEntry{{
		Labels:   map[string]string{labelCluster: someCluster},
		Counters: map[string]uint64{"a": 1},
	}}}
	bb, err := json.Marshal(perm)
	reqs.NoError(err)
	reqs.NoError(os.WriteFile(f.permanentPath(), bb, 0o644))

	tmp := fileState{Clusters: []clusterEntry{{
		Labels:   map[string]string{labelCluster: someCluster},
		Counters: map[string]uint64{"a": 1, "b": 2},
	}}}
	bb, err = json.Marshal(tmp)
	reqs.NoError(err)
	reqs.NoError(os.WriteFile(f.tempPath(), bb, 0o644))

	m, err := f.readMergedOnDisk()
	reqs.NoError(err)
	reqs.Len(m, 1, "must hold only one cluster")
	reqs.Equal(someCluster, m[someCluster].Labels[labelCluster])
	reqs.Equal(uint64(2), m[someCluster].Counters["a"])
	reqs.Equal(uint64(2), m[someCluster].Counters["b"])
}

func Test_flush(t *testing.T) {
	t.Run("non_empty", func(t *testing.T) {
		reqs := require.New(t)
		f := mustNewFile(t)

		const (
			someCluster      = "ns/name"
			someOtherCluster = "ns/other-name"
			someLabel        = "someLabel"
		)

		perm := fileState{Clusters: []clusterEntry{{
			Labels:   map[string]string{labelCluster: someCluster},
			Counters: map[string]uint64{"a": 1},
		}}}
		bb, err := json.Marshal(perm)
		reqs.NoError(err)
		reqs.NoError(os.WriteFile(f.permanentPath(), bb, 0o644))

		entries := map[string]clusterEntry{
			someCluster:      {Labels: map[string]string{labelCluster: someCluster}, Counters: map[string]uint64{"a": 2}},
			someOtherCluster: {Labels: map[string]string{labelCluster: someOtherCluster}, Counters: map[string]uint64{"x": 9}},
		}

		reqs.NoError(f.flush(entries))
		reqs.Empty(entries, "flush should clear the in-memory map")

		// read merged
		data, err := os.ReadFile(f.tempPath())
		reqs.NoError(err)
		var st fileState
		reqs.NoError(json.Unmarshal(data, &st))

		m := stateToMap(st)
		reqs.Equal(uint64(3), m[someCluster].Counters["a"])
		reqs.Equal(uint64(9), m[someOtherCluster].Counters["x"])
	})

	t.Run("empty_entries_rotate", func(t *testing.T) {
		reqs := require.New(t)
		f := mustNewFile(t)

		yesterday := dateOnlyTime(time.Now().Add(-24 * time.Hour))
		prevTemp := filepath.Join(f.baseDir, "telemetry-"+yesterday.Format(time.DateOnly)+".json.tmp")
		reqs.NoError(os.WriteFile(prevTemp, []byte("{}"), 0o644))

		f.today = yesterday

		reqs.NoError(f.flush(nil))

		prevFinal := filepath.Join(f.baseDir, yesterday.Format(time.DateOnly)+".json")
		_, err := os.Stat(prevFinal)
		reqs.NoError(err, "yesterday temp should have been renamed to permanent")
	})
}

func Test_rotateIfNeededLocked(t *testing.T) {
	t.Parallel()

	reqs := require.New(t)
	f := mustNewFile(t)

	today := f.today
	yesterday := dateOnlyTime(today.Add(-24 * time.Hour))
	prevTemp := filepath.Join(f.baseDir, "telemetry-"+yesterday.Format(time.DateOnly)+".json.tmp")
	reqs.NoError(os.WriteFile(prevTemp, []byte("{}"), 0o644))

	f.today = yesterday
	reqs.NoError(f.rotateIfNeededLocked())

	prevFinal := filepath.Join(f.baseDir, yesterday.Format(time.DateOnly)+".json")
	_, err := os.Stat(prevFinal)
	reqs.NoError(err, "yesterday final should exist after rotate")

	_, err = os.Stat(f.tempPath())
	reqs.NoError(err, "today temp should be opened after rotate")
	reqs.Contains(f.tempPath(), "telemetry-"+today.Format(time.DateOnly)+".json.tmp", "today temp must follow pattern")
}

func Test_closeAndRotate(t *testing.T) {
	t.Run("same_day", func(t *testing.T) {
		reqs := require.New(t)
		f := mustNewFile(t)

		reqs.NoError(os.WriteFile(f.tempPath(), []byte("{}"), 0o644))
		reqs.NoError(f.closeAndRotate())
		_, err := os.Stat(f.permanentPath())
		reqs.ErrorIs(err, fs.ErrNotExist, "permanent should not exist after same-day close")
	})

	t.Run("day_change", func(t *testing.T) {
		reqs := require.New(t)
		f := mustNewFile(t)

		yesterday := dateOnlyTime(f.today.Add(-24 * time.Hour))
		f.today = yesterday

		prevTemp := filepath.Join(f.baseDir, "telemetry-"+yesterday.Format(time.DateOnly)+".json.tmp")
		reqs.NoError(os.WriteFile(prevTemp, []byte(""), 0o644))
		reqs.NoError(f.closeAndRotate())

		prevFinal := filepath.Join(f.baseDir, yesterday.Format(time.DateOnly)+".json")
		_, err := os.Stat(prevFinal)
		reqs.ErrorIs(err, fs.ErrNotExist, "empty temp should not be renamed on close")

		reqs.NoError(os.WriteFile(prevTemp, []byte("{}"), 0o644))
		reqs.NoError(f.closeAndRotate())
		_, err = os.Stat(prevFinal)
		reqs.NoError(err, "non-empty temp should be renamed on close")
	})
}
