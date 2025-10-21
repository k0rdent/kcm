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
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_getPartialCAPIClusters(t *testing.T) {
	cl1, cl2 := &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "capi1"},
	}, &clusterapiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "capi2"},
	}

	reqs := require.New(t)
	scheme := runtime.NewScheme()
	reqs.NoError(metav1.AddMetaToScheme(scheme))
	reqs.NoError(clusterapiv1.AddToScheme(scheme))

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cl1, cl2).Build()
	capiClusters, err := getPartialCAPIClusters(t.Context(), mgmtClient)
	reqs.NoError(err)
	reqs.Len(capiClusters, 2)
	names := []string{capiClusters[0].Name, capiClusters[1].Name}
	reqs.Equal([]string{"capi1", "capi2"}, names)
}
