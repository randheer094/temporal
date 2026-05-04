package rules

import (
	"encoding/json"
	"fmt"

	"go.starlark.net/starlark"
)

func jsonToStarlark(data []byte) (starlark.Value, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return interfaceToStarlark(v)
}

func interfaceToStarlark(v interface{}) (starlark.Value, error) {
	if v == nil {
		return starlark.None, nil
	}
	switch val := v.(type) {
	case bool:
		return starlark.Bool(val), nil
	case float64:
		if val == float64(int64(val)) {
			return starlark.MakeInt64(int64(val)), nil
		}
		return starlark.Float(val), nil
	case string:
		return starlark.String(val), nil
	case []interface{}:
		elems := make([]starlark.Value, len(val))
		for i, item := range val {
			sv, err := interfaceToStarlark(item)
			if err != nil {
				return nil, err
			}
			elems[i] = sv
		}
		return starlark.NewList(elems), nil
	case map[string]interface{}:
		d := new(starlark.Dict)
		for k, item := range val {
			sv, err := interfaceToStarlark(item)
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, fmt.Errorf("set key %q: %w", k, err)
			}
		}
		return d, nil
	default:
		return starlark.None, nil
	}
}
