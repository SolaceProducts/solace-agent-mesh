package jmespath

import (
	"errors"
	"reflect"
)

func isFalse(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return !v
	case []interface{}:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	case string:
		return len(v) == 0
	case nil:
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Struct:
		return false
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	case reflect.Ptr:
		if rv.IsNil() {
			return true
		}
		element := rv.Elem()
		return isFalse(element.Interface())
	}
	return false
}

func objsEqual(left interface{}, right interface{}) bool {
	return reflect.DeepEqual(left, right)
}

type sliceParam struct {
	N         int
	Specified bool
}

func slice(slice []interface{}, parts []sliceParam) ([]interface{}, error) {
	computed, err := computeSliceParams(len(slice), parts)
	if err != nil {
		return nil, err
	}
	start, stop, step := computed[0], computed[1], computed[2]
	result := []interface{}{}
	if step > 0 {
		for i := start; i < stop; i += step {
			result = append(result, slice[i])
		}
	} else {
		for i := start; i > stop; i += step {
			result = append(result, slice[i])
		}
	}
	return result, nil
}

func computeSliceParams(length int, parts []sliceParam) ([]int, error) {
	var start, stop, step int
	if !parts[2].Specified {
		step = 1
	} else if parts[2].N == 0 {
		return nil, errors.New("Invalid slice, step cannot be 0")
	} else {
		step = parts[2].N
	}
	var stepValueNegative bool
	if step < 0 {
		stepValueNegative = true
	} else {
		stepValueNegative = false
	}

	if !parts[0].Specified {
		if stepValueNegative {
			start = length - 1
		} else {
			start = 0
		}
	} else {
		start = capSlice(length, parts[0].N, step)
	}

	if !parts[1].Specified {
		if stepValueNegative {
			stop = -1
		} else {
			stop = length
		}
	} else {
		stop = capSlice(length, parts[1].N, step)
	}
	return []int{start, stop, step}, nil
}

func capSlice(length int, actual int, step int) int {
	if actual < 0 {
		actual += length
		if actual < 0 {
			if step < 0 {
				actual = -1
			} else {
				actual = 0
			}
		}
	} else if actual >= length {
		if step < 0 {
			actual = length - 1
		} else {
			actual = length
		}
	}
	return actual
}

func toArrayNum(data interface{}) ([]float64, bool) {
	if d, ok := data.([]interface{}); ok {
		result := make([]float64, len(d))
		for i, el := range d {
			item, ok := el.(float64)
			if !ok {
				return nil, false
			}
			result[i] = item
		}
		return result, true
	}
	return nil, false
}

func toArrayStr(data interface{}) ([]string, bool) {
	if d, ok := data.([]interface{}); ok {
		result := make([]string, len(d))
		for i, el := range d {
			item, ok := el.(string)
			if !ok {
				return nil, false
			}
			result[i] = item
		}
		return result, true
	}
	return nil, false
}

func isSliceType(v interface{}) bool {
	if v == nil {
		return false
	}
	return reflect.TypeOf(v).Kind() == reflect.Slice
}
