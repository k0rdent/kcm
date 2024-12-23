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

package credspropagation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func convertAnyToLuaValue(l *lua.LState, v any) lua.LValue {
	if l == nil {
		return lua.LNil
	}

	switch val := v.(type) {
	case string:
		return lua.LString(val)
	case []byte:
		return lua.LString(string(val))
	case json.RawMessage:
		return lua.LString(string(val))
	case int:
		return lua.LNumber(val)
	case uint:
		return lua.LNumber(val)
	case float32:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case bool:
		return lua.LBool(val)
	case []any:
		arr := l.NewTable()

		for i, item := range val {
			arr.RawSetInt(i+1, convertAnyToLuaValue(l, item))
		}

		return arr
	case map[string]any:
		return convertMapToLuaTable(l, val)
	case map[int]any:
		table := l.NewTable()

		for k, v := range val {
			table.RawSetInt(k, convertAnyToLuaValue(l, v))
		}

		return table
	case nil:
		return lua.LNil
	default:
		return lua.LNil
	}
}

func convertMapToLuaTable(l *lua.LState, m map[string]any) *lua.LTable {
	if l == nil || m == nil {
		return nil
	}

	table := l.NewTable()

	for k, v := range m {
		table.RawSetString(k, convertAnyToLuaValue(l, v))
	}

	return table
}

func convertLuaValueToAny(value lua.LValue) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case lua.LString:
		return string(v)
	case lua.LNumber:
		return float64(v)
	case lua.LBool:
		return bool(v)
	case *lua.LTable:
		if v == nil {
			return nil
		}

		if v.MaxN() > 0 {
			arr := make([]any, 0, v.MaxN())

			v.ForEach(func(_, item lua.LValue) {
				arr = append(arr, convertLuaValueToAny(item))
			})

			return arr
		}

		return convertLuaTableToMap(v)
	default:
		return nil
	}
}

func convertLuaTableToMap(table *lua.LTable) map[string]any {
	if table == nil {
		return nil
	}

	result := make(map[string]any)

	table.ForEach(func(key, value lua.LValue) {
		switch v := value.(type) {
		case lua.LString:
			result[key.String()] = string(v)
		case lua.LNumber:
			result[key.String()] = float64(v)
		case lua.LBool:
			result[key.String()] = bool(v)
		case *lua.LTable:
			if v == nil {
				return
			}

			if v.MaxN() > 0 {
				arr := make([]any, 0, v.MaxN())

				v.ForEach(func(_, item lua.LValue) {
					arr = append(arr, convertLuaValueToAny(item))
				})

				result[key.String()] = arr
			} else {
				result[key.String()] = convertLuaTableToMap(v)
			}
		default:
			result[key.String()] = nil
		}
	})

	return result
}

func getGVKFromTable(table *lua.LTable) schema.GroupVersionKind {
	if table == nil {
		return schema.GroupVersionKind{}
	}

	group := table.RawGetString("group")
	version := table.RawGetString("version")
	kind := table.RawGetString("kind")

	if group == lua.LNil || version == lua.LNil || kind == lua.LNil {
		return schema.GroupVersionKind{}
	}

	return schema.GroupVersionKind{
		Group:   group.String(),
		Version: version.String(),
		Kind:    kind.String(),
	}
}

func getObject(ctx context.Context, kubeClient client.Client, l *lua.LState) int {
	if l == nil {
		l.Push(lua.LNil)
		l.Push(lua.LString("undefined state"))
		return 2
	}

	if l.GetTop() != 3 {
		l.Push(lua.LNil)
		l.Push(lua.LString("invalid number of arguments"))
		return 2
	}

	gvkTable := l.CheckTable(1)
	if gvkTable == nil {
		l.Push(lua.LNil)
		l.Push(lua.LString("invalid GVK table"))
		return 2
	}

	objectName, objectNamespace := l.CheckString(2), l.CheckString(3)
	if objectName == "" {
		l.Push(lua.LNil)
		l.Push(lua.LString("empty object Name"))
		return 2
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(getGVKFromTable(gvkTable))

	if err := kubeClient.Get(ctx, client.ObjectKey{
		Name:      objectName,
		Namespace: objectNamespace,
	}, obj); err != nil {
		l.Push(lua.LNil)
		l.Push(lua.LString(fmt.Sprintf("failed to get object %s/%s of kind %s: %v",
			objectNamespace, objectName, obj.GetObjectKind().GroupVersionKind().Kind, err)))
		return 2
	}

	l.Push(convertMapToLuaTable(l, obj.UnstructuredContent()))
	return 1
}

func jsonEncode(l *lua.LState) int {
	if l == nil {
		l.Push(lua.LNil)
		l.Push(lua.LString("undefined state"))
		return 2
	}

	var val any

	switch v := l.CheckAny(1).(type) {
	case *lua.LTable:
		if v == nil {
			l.Push(lua.LNil)
			l.Push(lua.LString("nil table provided for JSON encoding"))
			return 2
		}

		val = convertLuaTableToMap(v)
	default:
		l.Push(lua.LNil)
		l.Push(lua.LString(fmt.Sprintf("unsupported type for JSON encoding: %T", v)))
		return 2
	}

	b, err := json.Marshal(val)
	if err != nil {
		l.Push(lua.LNil)
		l.Push(lua.LString(err.Error()))
		return 2
	}

	l.Push(lua.LString(string(b)))
	return 1
}

func base64Encode(l *lua.LState) int {
	if l == nil {
		l.Push(lua.LNil)
		l.Push(lua.LString("undefined state"))
		return 2
	}

	str := l.CheckString(1)
	if str == "" {
		l.Push(lua.LNil)
		l.Push(lua.LString("empty string provided for base64 encoding"))
		return 2
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(str))
	l.Push(lua.LString(encoded))
	return 1
}

func getProviderObjects(ctx context.Context, kubeClient client.Client, namespace, name, luaCode string) ([]client.Object, error) {
	if namespace == "" || name == "" {
		return nil, errors.New("namespace and name must not be empty")
	}

	if kubeClient == nil {
		return nil, errors.New("kubeClient must not be nil")
	}

	l := lua.NewState()
	defer l.Close()

	l.SetGlobal("getObject", l.NewFunction(func(l *lua.LState) int {
		return getObject(ctx, kubeClient, l)
	}))

	l.SetGlobal("jsonEncode", l.NewFunction(jsonEncode))
	l.SetGlobal("base64Encode", l.NewFunction(base64Encode))

	if err := l.DoString(luaCode); err != nil {
		return nil, err
	}

	if err := l.CallByParam(lua.P{
		Fn:      l.GetGlobal("getObjects"),
		NRet:    1,
		Protect: true,
	}, lua.LString(namespace), lua.LString(name)); err != nil {
		return nil, err
	}

	result := l.Get(-1)
	l.Pop(1)

	if result == lua.LNil {
		return nil, nil
	}

	resultTable, ok := result.(*lua.LTable)
	if !ok {
		return nil, errors.New("result is not Lua Table")
	}

	var objects []client.Object

	resultTable.ForEach(func(_, value lua.LValue) {
		objTable, ok := value.(*lua.LTable)
		if !ok {
			return
		}

		u := &unstructured.Unstructured{
			Object: convertLuaTableToMap(objTable),
		}

		if u.GetName() == "" {
			return // skip objects without a name
		}

		if u.GetKind() == "" {
			return // skip objects without a kind
		}

		objects = append(objects, u)
	})

	return objects, nil
}

func PropagateProviderObjects(ctx context.Context, cfg *PropagationCfg, luaCode string) error {
	if cfg == nil || luaCode == "" {
		return nil
	}

	objects, err := getProviderObjects(ctx, cfg.Client,
		cfg.ClusterDeployment.Namespace, cfg.ClusterDeployment.Name, luaCode,
	)
	if err != nil {
		return fmt.Errorf("failed to get Azure CCM objects: %w", err)
	}

	if err := applyCCMConfigs(ctx, cfg.KubeconfSecret, objects...); err != nil {
		return fmt.Errorf("failed to apply Azure CCM objects: %w", err)
	}

	return nil
}
