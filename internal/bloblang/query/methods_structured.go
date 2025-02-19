package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Jeffail/gabs/v2"
	jsonschema "github.com/xeipuuv/gojsonschema"
)

var _ = registerSimpleMethod(
	NewMethodSpec(
		"all",
		"Checks each element of an array against a query and returns true if all elements passed. An error occurs if the target is not an array, or if any element results in the provided query returning a non-boolean result. Returns false if the target array is empty.",
	).InCategory(
		MethodCategoryObjectAndArray,
		"",
		NewExampleSpec("",
			`root.all_over_21 = this.patrons.all(patron -> patron.age >= 21)`,
			`{"patrons":[{"id":"1","age":18},{"id":"2","age":23}]}`,
			`{"all_over_21":false}`,
			`{"patrons":[{"id":"1","age":45},{"id":"2","age":23}]}`,
			`{"all_over_21":true}`,
		),
	).Param(ParamQuery("test", "A test query to apply to each element.")),
	func(args *ParsedParams) (simpleMethod, error) {
		queryFn, err := args.FieldQuery("test")
		if err != nil {
			return nil, err
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			arr, ok := res.([]interface{})
			if !ok {
				return nil, NewTypeError(res, ValueArray)
			}
			if len(arr) == 0 {
				return false, nil
			}
			for i, v := range arr {
				res, err := queryFn.Exec(ctx.WithValue(v))
				if err != nil {
					return nil, fmt.Errorf("element %v: %w", i, err)
				}
				b, ok := res.(bool)
				if !ok {
					return nil, fmt.Errorf("element %v: %w", i, NewTypeError(res, ValueBool))
				}
				if !b {
					return false, nil
				}
			}
			return true, nil
		}, nil
	},
)

var _ = registerSimpleMethod(
	NewMethodSpec(
		"any",
		"Checks the elements of an array against a query and returns true if any element passes. An error occurs if the target is not an array, or if an element results in the provided query returning a non-boolean result. Returns false if the target array is empty.",
	).InCategory(
		MethodCategoryObjectAndArray,
		"",
		NewExampleSpec("",
			`root.any_over_21 = this.patrons.any(patron -> patron.age >= 21)`,
			`{"patrons":[{"id":"1","age":18},{"id":"2","age":23}]}`,
			`{"any_over_21":true}`,
			`{"patrons":[{"id":"1","age":10},{"id":"2","age":12}]}`,
			`{"any_over_21":false}`,
		),
	).Param(ParamQuery("test", "A test query to apply to each element.")),
	func(args *ParsedParams) (simpleMethod, error) {
		queryFn, err := args.FieldQuery("test")
		if err != nil {
			return nil, err
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			arr, ok := res.([]interface{})
			if !ok {
				return nil, NewTypeError(res, ValueArray)
			}

			if len(arr) == 0 {
				return false, nil
			}

			for i, v := range arr {
				res, err := queryFn.Exec(ctx.WithValue(v))
				if err != nil {
					return nil, fmt.Errorf("element %v: %w", i, err)
				}
				b, ok := res.(bool)
				if !ok {
					return nil, fmt.Errorf("element %v: %w", i, NewTypeError(res, ValueBool))
				}
				if b {
					return true, nil
				}
			}

			return false, nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"append",
		"Returns an array with new elements appended to the end.",
	).InCategory(
		MethodCategoryObjectAndArray,
		"",
		NewExampleSpec("",
			`root.foo = this.foo.append("and", "this")`,
			`{"foo":["bar","baz"]}`,
			`{"foo":["bar","baz","and","this"]}`,
		),
	).VariadicParams(),
	func(args *ParsedParams) (simpleMethod, error) {
		argsList := args.Raw()
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			arr, ok := res.([]interface{})
			if !ok {
				return nil, NewTypeError(res, ValueArray)
			}
			copied := make([]interface{}, 0, len(arr)+len(argsList))
			copied = append(copied, arr...)
			return append(copied, argsList...), nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"collapse", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Collapse an array or object into an object of key/value pairs for each field, where the key is the full path of the structured field in dot path notation. Empty arrays an objects are ignored by default.",
		NewExampleSpec("",
			`root.result = this.collapse()`,
			`{"foo":[{"bar":"1"},{"bar":{}},{"bar":"2"},{"bar":[]}]}`,
			`{"result":{"foo.0.bar":"1","foo.2.bar":"2"}}`,
		),
		NewExampleSpec(
			"An optional boolean parameter can be set to true in order to include empty objects and arrays.",
			`root.result = this.collapse(true)`,
			`{"foo":[{"bar":"1"},{"bar":{}},{"bar":"2"},{"bar":[]}]}`,
			`{"result":{"foo.0.bar":"1","foo.1.bar":{},"foo.2.bar":"2","foo.3.bar":[]}}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		includeEmpty := false
		if len(args) > 0 {
			includeEmpty = args[0].(bool)
		}
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			gObj := gabs.Wrap(v)
			if includeEmpty {
				return gObj.FlattenIncludeEmpty()
			}
			return gObj.Flatten()
		}, nil
	},
	true,
	oldParamsExpectOneOrZeroArgs(),
	oldParamsExpectBoolArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"contains", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Checks whether an array contains an element matching the argument, or an object contains a value matching the argument, and returns a boolean result. Numerical comparisons are made irrespective of the representation type (float versus integer).",
		NewExampleSpec("",
			`root.has_foo = this.thing.contains("foo")`,
			`{"thing":["this","foo","that"]}`,
			`{"has_foo":true}`,
			`{"thing":["this","bar","that"]}`,
			`{"has_foo":false}`,
		),
		NewExampleSpec("",
			`root.has_bar = this.thing.contains(20)`,
			`{"thing":[10.3,20.0,"huh",3]}`,
			`{"has_bar":true}`,
			`{"thing":[2,3,40,67]}`,
			`{"has_bar":false}`,
		),
	).InCategory(
		MethodCategoryStrings,
		"Checks whether a string contains a substring and returns a boolean result.",
		NewExampleSpec("",
			`root.has_foo = this.thing.contains("foo")`,
			`{"thing":"this foo that"}`,
			`{"has_foo":true}`,
			`{"thing":"this bar that"}`,
			`{"has_foo":false}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		compareRight := args[0]
		compareFn := func(compareLeft interface{}) bool {
			return compareRight == compareLeft
		}
		if compareRightNum, err := IGetNumber(args[0]); err == nil {
			compareFn = func(compareLeft interface{}) bool {
				if leftAsNum, err := IGetNumber(compareLeft); err == nil {
					return leftAsNum == compareRightNum
				}
				return false
			}
		}
		sub := IToString(args[0])
		bsub := IToBytes(args[0])
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			switch t := v.(type) {
			case string:
				return strings.Contains(t, sub), nil
			case []byte:
				return bytes.Contains(t, bsub), nil
			case []interface{}:
				for _, compareLeft := range t {
					if compareFn(compareLeft) {
						return true, nil
					}
				}
			case map[string]interface{}:
				for _, compareLeft := range t {
					if compareFn(compareLeft) {
						return true, nil
					}
				}
			default:
				return nil, NewTypeError(v, ValueString, ValueArray, ValueObject)
			}
			return false, nil
		}, nil
	},
	true,
	oldParamsExpectNArgs(1),
)

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"enumerated",
		"Converts an array into a new array of objects, where each object has a field index containing the `index` of the element and a field `value` containing the original value of the element.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec("",
			`root.foo = this.foo.enumerated()`,
			`{"foo":["bar","baz"]}`,
			`{"foo":[{"index":0,"value":"bar"},{"index":1,"value":"baz"}]}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			arr, ok := v.([]interface{})
			if !ok {
				return nil, NewTypeError(v, ValueArray)
			}
			enumerated := make([]interface{}, 0, len(arr))
			for i, ele := range arr {
				enumerated = append(enumerated, map[string]interface{}{
					"index": int64(i),
					"value": ele,
				})
			}
			return enumerated, nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"exists",
		"Checks that a field, identified via a [dot path][field_paths], exists in an object.",
		NewExampleSpec("",
			`root.result = this.foo.exists("bar.baz")`,
			`{"foo":{"bar":{"baz":"yep, I exist"}}}`,
			`{"result":true}`,
			`{"foo":{"bar":{}}}`,
			`{"result":false}`,
			`{"foo":{}}`,
			`{"result":false}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		pathStr := args[0].(string)
		path := gabs.DotPathToSlice(pathStr)
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			return gabs.Wrap(v).Exists(path...), nil
		}, nil
	},
	true,
	oldParamsExpectNArgs(1),
	oldParamsExpectStringArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"explode", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Explodes an array or object at a [field path][field_paths].",
		NewExampleSpec(`##### On arrays

Exploding arrays results in an array containing elements matching the original document, where the target field of each element is an element of the exploded array:`,
			`root = this.explode("value")`,
			`{"id":1,"value":["foo","bar","baz"]}`,
			`[{"id":1,"value":"foo"},{"id":1,"value":"bar"},{"id":1,"value":"baz"}]`,
		),
		NewExampleSpec(`##### On objects

Exploding objects results in an object where the keys match the target object, and the values match the original document but with the target field replaced by the exploded value:`,
			`root = this.explode("value")`,
			`{"id":1,"value":{"foo":2,"bar":[3,4],"baz":{"bev":5}}}`,
			`{"bar":{"id":1,"value":[3,4]},"baz":{"id":1,"value":{"bev":5}},"foo":{"id":1,"value":2}}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		pathRaw := args[0].(string)
		path := gabs.DotPathToSlice(pathRaw)
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			target := gabs.Wrap(v).Search(path...)

			switch t := target.Data().(type) {
			case []interface{}:
				result := make([]interface{}, len(t))
				for i, ele := range t {
					gExploded := gabs.Wrap(IClone(v))
					gExploded.Set(ele, path...)
					result[i] = gExploded.Data()
				}
				return result, nil
			case map[string]interface{}:
				result := make(map[string]interface{}, len(t))
				for key, ele := range t {
					gExploded := gabs.Wrap(IClone(v))
					gExploded.Set(ele, path...)
					result[key] = gExploded.Data()
				}
				return result, nil
			}

			return nil, fmt.Errorf("expected array or object value at path '%v', found: %v", pathRaw, ITypeOf(target.Data()))
		}, nil
	},
	true,
	oldParamsExpectNArgs(1),
	oldParamsExpectStringArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"filter", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Executes a mapping query argument for each element of an array or key/value pair of an object. If the query returns `false` the item is removed from the resulting array or object. The item will also be removed if the query returns any non-boolean value.",
		NewExampleSpec(``,
			`root.new_nums = this.nums.filter(num -> num > 10)`,
			`{"nums":[3,11,4,17]}`,
			`{"new_nums":[11,17]}`,
		),
		NewExampleSpec(`##### On objects

When filtering objects the mapping query argument is provided a context with a field `+"`key`"+` containing the value key, and a field `+"`value`"+` containing the value.`,
			`root.new_dict = this.dict.filter(item -> item.value.contains("foo"))`,
			`{"dict":{"first":"hello foo","second":"world","third":"this foo is great"}}`,
			`{"new_dict":{"first":"hello foo","third":"this foo is great"}}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		mapFn, ok := args[0].(Function)
		if !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[0])
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			var resValue interface{}
			switch t := res.(type) {
			case []interface{}:
				newSlice := make([]interface{}, 0, len(t))
				for _, v := range t {
					f, err := mapFn.Exec(ctx.WithValue(v))
					if err != nil {
						return nil, err
					}
					if b, _ := f.(bool); b {
						newSlice = append(newSlice, v)
					}
				}
				resValue = newSlice
			case map[string]interface{}:
				newMap := make(map[string]interface{}, len(t))
				for k, v := range t {
					var ctxMap interface{} = map[string]interface{}{
						"key":   k,
						"value": v,
					}
					f, err := mapFn.Exec(ctx.WithValue(ctxMap))
					if err != nil {
						return nil, err
					}
					if b, _ := f.(bool); b {
						newMap[k] = v
					}
				}
				resValue = newMap
			default:
				return nil, NewTypeError(res, ValueArray, ValueObject)
			}
			return resValue, nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(1),
	oldParamsExpectFunctionArg(0),
)

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"flatten",
		"Iterates an array and any element that is itself an array is removed and has its elements inserted directly in the resulting array.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec(``,
			`root.result = this.flatten()`,
			`["foo",["bar","baz"],"buz"]`,
			`{"result":["foo","bar","baz","buz"]}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			array, isArray := v.([]interface{})
			if !isArray {
				return nil, NewTypeError(v, ValueArray)
			}
			result := make([]interface{}, 0, len(array))
			for _, child := range array {
				switch t := child.(type) {
				case []interface{}:
					result = append(result, t...)
				default:
					result = append(result, t)
				}
			}
			return result, nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"fold",
		"Takes two arguments: an initial value, and a mapping query. For each element of an array the mapping context is an object with two fields `tally` and `value`, where `tally` contains the current accumulated value and `value` is the value of the current element. The mapping must return the result of adding the value to the tally.\n\nThe first argument is the value that `tally` will have on the first call.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec(``,
			`root.sum = this.foo.fold(0, item -> item.tally + item.value)`,
			`{"foo":[3,8,11]}`,
			`{"sum":22}`,
		),
		NewExampleSpec(``,
			`root.result = this.foo.fold("", item -> "%v%v".format(item.tally, item.value))`,
			`{"foo":["hello ", "world"]}`,
			`{"result":"hello world"}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		var foldTallyStart interface{}
		switch t := args[0].(type) {
		case *Literal:
			foldTallyStart = t.Value
		default:
			foldTallyStart = t
		}
		foldFn, ok := args[1].(Function)
		if !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[1])
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			resArray, ok := res.([]interface{})
			if !ok {
				return nil, NewTypeError(res, ValueArray)
			}

			var tally interface{}
			var err error
			switch t := foldTallyStart.(type) {
			case Function:
				if tally, err = t.Exec(ctx); err != nil {
					return nil, fmt.Errorf("failed to extract tally initial value: %w", err)
				}
			default:
				tally = IClone(foldTallyStart)
			}

			for _, v := range resArray {
				newV, mapErr := foldFn.Exec(ctx.WithValue(map[string]interface{}{
					"tally": tally,
					"value": v,
				}))
				if mapErr != nil {
					return nil, mapErr
				}
				tally = newV
			}
			return tally, nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(2),
	oldParamsExpectFunctionArg(1),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"index",
		"Extract an element from an array by an index. The index can be negative, and if so the element will be selected from the end counting backwards starting from -1. E.g. an index of -1 returns the last element, an index of -2 returns the element before the last, and so on.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec("",
			`root.last_name = this.names.index(-1)`,
			`{"names":["rachel","stevens"]}`,
			`{"last_name":"stevens"}`,
		),
		NewExampleSpec("It is also possible to use this method on byte arrays, in which case the selected element will be returned as an integer.",
			`root.last_byte = this.name.bytes().index(-1)`,
			`{"name":"foobar bazson"}`,
			`{"last_byte":110}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		index := args[0].(int64)
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			switch array := v.(type) {
			case []interface{}:
				i := int(index)
				if i < 0 {
					i = len(array) + i
				}
				if i < 0 || i >= len(array) {
					return nil, fmt.Errorf("index '%v' was out of bounds for array size: %v", i, len(array))
				}
				return array[i], nil
			case []byte:
				i := int(index)
				if i < 0 {
					i = len(array) + i
				}
				if i < 0 || i >= len(array) {
					return nil, fmt.Errorf("index '%v' was out of bounds for array size: %v", i, len(array))
				}
				return int64(array[i]), nil
			default:
				return nil, NewTypeError(v, ValueArray)
			}
		}, nil
	},
	true,
	oldParamsExpectNArgs(1),
	oldParamsExpectIntArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"json_schema",
		"Checks a [JSON schema](https://json-schema.org/) against a value and returns the value if it matches or throws and error if it does not.",
	).InCategory(
		MethodCategoryObjectAndArray,
		"",
		NewExampleSpec("",
			`root = this.json_schema("""{
  "type":"object",
  "properties":{
    "foo":{
      "type":"string"
    }
  }
}""")`,
			`{"foo":"bar"}`,
			`{"foo":"bar"}`,
			`{"foo":5}`,
			`Error("failed assignment (line 1): field `+"`this`"+`: foo invalid type. expected: string, given: integer")`,
		),
		NewExampleSpec(
			"In order to load a schema from a file use the `file` function.",
			`root = this.json_schema(file(var("BENTHOS_TEST_BLOBLANG_SCHEMA_FILE")))`,
		),
	).Beta(),
	func(args ...interface{}) (simpleMethod, error) {
		schema, err := jsonschema.NewSchema(jsonschema.NewStringLoader(args[0].(string)))
		if err != nil {
			return nil, fmt.Errorf("failed to parse json schema definition: %w", err)
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			result, err := schema.Validate(jsonschema.NewGoLoader(res))
			if err != nil {
				return nil, err
			}
			if !result.Valid() {
				var errStr string
				for i, desc := range result.Errors() {
					if i > 0 {
						errStr = errStr + "\n"
					}
					description := strings.ToLower(desc.Description())
					if property := desc.Details()["property"]; property != nil {
						description = property.(string) + strings.TrimPrefix(description, strings.ToLower(property.(string)))
					}
					errStr = errStr + desc.Field() + " " + description
				}
				return nil, errors.New(errStr)
			}
			return res, nil
		}, nil
	},
	true,
	oldParamsExpectNArgs(1),
	oldParamsExpectStringArg(0),
)

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"keys",
		"Returns the keys of an object as an array.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec("",
			`root.foo_keys = this.foo.keys()`,
			`{"foo":{"bar":1,"baz":2}}`,
			`{"foo_keys":["bar","baz"]}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			if m, ok := v.(map[string]interface{}); ok {
				keys := make([]interface{}, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Slice(keys, func(i, j int) bool {
					return keys[i].(string) < keys[j].(string)
				})
				return keys, nil
			}
			return nil, NewTypeError(v, ValueObject)
		}, nil
	},
)

var _ = registerSimpleMethod(
	NewMethodSpec(
		"key_values",
		"Returns the key/value pairs of an object as an array, where each element is an object with a `key` field and a `value` field. The order of the resulting array will be random.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec("",
			`root.foo_key_values = this.foo.key_values().sort_by(pair -> pair.key)`,

			`{"foo":{"bar":1,"baz":2}}`,
			`{"foo_key_values":[{"key":"bar","value":1},{"key":"baz","value":2}]}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			if m, ok := v.(map[string]interface{}); ok {
				keyValues := make([]interface{}, 0, len(m))
				for k, v := range m {
					keyValues = append(keyValues, map[string]interface{}{
						"key":   k,
						"value": v,
					})
				}
				return keyValues, nil
			}
			return nil, NewTypeError(v, ValueObject)
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"length", "",
	).InCategory(
		MethodCategoryStrings, "Returns the length of a string.",
		NewExampleSpec("",
			`root.foo_len = this.foo.length()`,
			`{"foo":"hello world"}`,
			`{"foo_len":11}`,
		),
	).InCategory(
		MethodCategoryObjectAndArray, "Returns the length of an array or object (number of keys).",
		NewExampleSpec("",
			`root.foo_len = this.foo.length()`,
			`{"foo":["first","second"]}`,
			`{"foo_len":2}`,
			`{"foo":{"first":"bar","second":"baz"}}`,
			`{"foo_len":2}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			var length int64
			switch t := v.(type) {
			case string:
				length = int64(len(t))
			case []byte:
				length = int64(len(t))
			case []interface{}:
				length = int64(len(t))
			case map[string]interface{}:
				length = int64(len(t))
			default:
				return nil, NewTypeError(v, ValueString, ValueArray, ValueObject)
			}
			return length, nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"map_each", "",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec(`##### On arrays

Apply a mapping to each element of an array and replace the element with the result. Within the argument mapping the context is the value of the element being mapped.`,
			`root.new_nums = this.nums.map_each(num -> if num < 10 {
  deleted()
} else {
  num - 10
})`,
			`{"nums":[3,11,4,17]}`,
			`{"new_nums":[1,7]}`,
		),
		NewExampleSpec(`##### On objects

Apply a mapping to each value of an object and replace the value with the result. Within the argument mapping the context is an object with a field `+"`key`"+` containing the value key, and a field `+"`value`"+`.`,
			`root.new_dict = this.dict.map_each(item -> item.value.uppercase())`,
			`{"dict":{"foo":"hello","bar":"world"}}`,
			`{"new_dict":{"bar":"WORLD","foo":"HELLO"}}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		mapFn, ok := args[0].(Function)
		if !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[0])
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			var resValue interface{}
			var err error
			switch t := res.(type) {
			case []interface{}:
				newSlice := make([]interface{}, 0, len(t))
				for i, v := range t {
					newV, mapErr := mapFn.Exec(ctx.WithValue(v))
					if mapErr != nil {
						return nil, fmt.Errorf("failed to process element %v: %w", i, ErrFrom(mapErr, mapFn))
					}
					switch newV.(type) {
					case Delete:
					case Nothing:
						newSlice = append(newSlice, v)
					default:
						newSlice = append(newSlice, newV)
					}
				}
				resValue = newSlice
			case map[string]interface{}:
				newMap := make(map[string]interface{}, len(t))
				for k, v := range t {
					var ctxMap interface{} = map[string]interface{}{
						"key":   k,
						"value": v,
					}
					newV, mapErr := mapFn.Exec(ctx.WithValue(ctxMap))
					if mapErr != nil {
						return nil, fmt.Errorf("failed to process element %v: %w", k, ErrFrom(mapErr, mapFn))
					}
					switch newV.(type) {
					case Delete:
					case Nothing:
						newMap[k] = v
					default:
						newMap[k] = newV
					}
				}
				resValue = newMap
			default:
				return nil, NewTypeError(res, ValueArray)
			}
			if err != nil {
				return nil, err
			}
			return resValue, nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(1),
	oldParamsExpectFunctionArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"map_each_key", "",
	).InCategory(
		MethodCategoryObjectAndArray, `Apply a mapping to each key of an object, and replace the key with the result, which must be a string.`,
		NewExampleSpec(``,
			`root.new_dict = this.dict.map_each_key(key -> key.uppercase())`,
			`{"dict":{"keya":"hello","keyb":"world"}}`,
			`{"new_dict":{"KEYA":"hello","KEYB":"world"}}`,
		),
		NewExampleSpec(``,
			`root = this.map_each_key(key -> if key.contains("kafka") { "_" + key })`,
			`{"amqp_key":"foo","kafka_key":"bar","kafka_topic":"baz"}`,
			`{"_kafka_key":"bar","_kafka_topic":"baz","amqp_key":"foo"}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		mapFn, ok := args[0].(Function)
		if !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[0])
		}
		return func(res interface{}, ctx FunctionContext) (interface{}, error) {
			obj, ok := res.(map[string]interface{})
			if !ok {
				return nil, NewTypeError(res, ValueObject)
			}

			newMap := make(map[string]interface{}, len(obj))
			for k, v := range obj {
				var ctxVal interface{} = k
				newKey, mapErr := mapFn.Exec(ctx.WithValue(ctxVal))
				if mapErr != nil {
					return nil, mapErr
				}

				switch t := newKey.(type) {
				// TODO: Revise whether we want this.
				// case Delete:
				case Nothing:
					newMap[k] = v
				case string:
					newMap[t] = v
				default:
					return nil, fmt.Errorf("unexpected result from key mapping: %w", NewTypeError(newKey, ValueString))
				}
			}
			return newMap, nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(1),
	oldParamsExpectFunctionArg(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"merge", "Merge a source object into an existing destination object. When a collision is found within the merged structures (both a source and destination object contain the same non-object keys) the result will be an array containing both values, where values that are already arrays will be expanded into the resulting array.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec(``,
			`root = this.foo.merge(this.bar)`,
			`{"foo":{"first_name":"fooer","likes":"bars"},"bar":{"second_name":"barer","likes":"foos"}}`,
			`{"first_name":"fooer","likes":["bars","foos"],"second_name":"barer"}`,
		),
	),
	false, mergeMethod,
	oldParamsExpectNArgs(1),
)

func mergeMethod(target Function, args ...interface{}) (Function, error) {
	var mapFn Function
	switch t := args[0].(type) {
	case Function:
		mapFn = t
	default:
		mapFn = NewLiteralFunction("", t)
	}
	return ClosureFunction("method merge", func(ctx FunctionContext) (interface{}, error) {
		mergeInto, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}

		mergeFrom, err := mapFn.Exec(ctx)
		if err != nil {
			return nil, err
		}

		if root, isArray := mergeInto.([]interface{}); isArray {
			if rhs, isAlsoArray := mergeFrom.([]interface{}); isAlsoArray {
				return append(root, rhs...), nil
			}
			return append(root, mergeFrom), nil
		}

		if _, isObject := mergeInto.(map[string]interface{}); !isObject {
			return nil, NewTypeErrorFrom(target.Annotation(), mergeInto, ValueObject, ValueArray)
		}

		root := gabs.New()
		if err = root.Merge(gabs.Wrap(mergeInto)); err == nil {
			err = root.Merge(gabs.Wrap(mergeFrom))
		}
		if err != nil {
			return nil, err
		}
		return root.Data(), nil
	}, aggregateTargetPaths(target, mapFn)), nil
}

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"not_empty", "",
	).InCategory(
		MethodCategoryCoercion,
		"Ensures that the given string, array or object value is not empty, and if so returns it, otherwise an error is returned.",
		NewExampleSpec("",
			`root.a = this.a.not_empty()`,
			`{"a":"foo"}`,
			`{"a":"foo"}`,

			`{"a":""}`,
			`Error("failed assignment (line 1): field `+"`this.a`"+`: string value is empty")`,

			`{"a":["foo","bar"]}`,
			`{"a":["foo","bar"]}`,

			`{"a":[]}`,
			`Error("failed assignment (line 1): field `+"`this.a`"+`: array value is empty")`,

			`{"a":{"b":"foo","c":"bar"}}`,
			`{"a":{"b":"foo","c":"bar"}}`,

			`{"a":{}}`,
			`Error("failed assignment (line 1): field `+"`this.a`"+`: object value is empty")`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			switch t := v.(type) {
			case string:
				if t == "" {
					return nil, errors.New("string value is empty")
				}
			case []interface{}:
				if len(t) == 0 {
					return nil, errors.New("array value is empty")
				}
			case map[string]interface{}:
				if len(t) == 0 {
					return nil, errors.New("object value is empty")
				}
			default:
				return nil, NewTypeError(v, ValueString, ValueArray, ValueObject)
			}
			return v, nil
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"sort", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Attempts to sort the values of an array in increasing order. The type of all values must match in order for the ordering to succeed. Supports string and number values.",
		NewExampleSpec("",
			`root.sorted = this.foo.sort()`,
			`{"foo":["bbb","ccc","aaa"]}`,
			`{"sorted":["aaa","bbb","ccc"]}`,
		),
		NewExampleSpec("It's also possible to specify a mapping argument, which is provided an object context with fields `left` and `right`, the mapping must return a boolean indicating whether the `left` value is less than `right`. This allows you to sort arrays containing non-string or non-number values.",
			`root.sorted = this.foo.sort(item -> item.left.v < item.right.v)`,
			`{"foo":[{"id":"foo","v":"bbb"},{"id":"bar","v":"ccc"},{"id":"baz","v":"aaa"}]}`,
			`{"sorted":[{"id":"baz","v":"aaa"},{"id":"foo","v":"bbb"},{"id":"bar","v":"ccc"}]}`,
		),
	),
	false, sortMethod,
	oldParamsExpectOneOrZeroArgs(),
	oldParamsExpectFunctionArg(0),
)

func sortMethod(target Function, args ...interface{}) (Function, error) {
	compareFn := func(ctx FunctionContext, values []interface{}, i, j int) (bool, error) {
		switch values[i].(type) {
		case float64, int, int64, uint64, json.Number:
			lhs, err := IGetNumber(values[i])
			if err != nil {
				return false, fmt.Errorf("sort element %v: %w", i, err)
			}
			rhs, err := IGetNumber(values[j])
			if err != nil {
				return false, fmt.Errorf("sort element %v: %w", j, err)
			}
			return lhs < rhs, nil
		case string, []byte:
			lhs, err := IGetString(values[i])
			if err != nil {
				return false, fmt.Errorf("sort element %v: %w", i, err)
			}
			rhs, err := IGetString(values[j])
			if err != nil {
				return false, fmt.Errorf("sort element %v: %w", j, err)
			}
			return lhs < rhs, nil
		}
		return false, fmt.Errorf("sort element %v: %w", i, NewTypeError(values[i], ValueNumber, ValueString))
	}
	var mapFn Function
	if len(args) > 0 {
		var ok bool
		if mapFn, ok = args[0].(Function); !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[0])
		}
		compareFn = func(ctx FunctionContext, values []interface{}, i, j int) (bool, error) {
			var ctxValue interface{} = map[string]interface{}{
				"left":  values[i],
				"right": values[j],
			}
			v, err := mapFn.Exec(ctx.WithValue(ctxValue))
			if err != nil {
				return false, err
			}
			b, ok := v.(bool)
			if !ok {
				return false, NewTypeErrorFrom("sort argument", v, ValueBool)
			}
			return b, nil
		}
	}

	targets := target.QueryTargets
	if mapFn != nil {
		targets = aggregateTargetPaths(target, mapFn)
	}

	return ClosureFunction("method sort", func(ctx FunctionContext) (interface{}, error) {
		v, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}
		if m, ok := v.([]interface{}); ok {
			values := make([]interface{}, 0, len(m))
			values = append(values, m...)

			sort.Slice(values, func(i, j int) bool {
				if err == nil {
					var b bool
					b, err = compareFn(ctx, values, i, j)
					return b
				}
				return false
			})
			if err != nil {
				return nil, err
			}
			return values, nil
		}
		return nil, NewTypeErrorFrom(target.Annotation(), v, ValueArray)
	}, targets), nil
}

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"sort_by", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Attempts to sort the elements of an array, in increasing order, by a value emitted by an argument query applied to each element. The type of all values must match in order for the ordering to succeed. Supports string and number values.",
		NewExampleSpec("",
			`root.sorted = this.foo.sort_by(ele -> ele.id)`,
			`{"foo":[{"id":"bbb","message":"bar"},{"id":"aaa","message":"foo"},{"id":"ccc","message":"baz"}]}`,
			`{"sorted":[{"id":"aaa","message":"foo"},{"id":"bbb","message":"bar"},{"id":"ccc","message":"baz"}]}`,
		),
	),
	false, sortByMethod,
	oldParamsExpectNArgs(1),
)

func sortByMethod(target Function, args ...interface{}) (Function, error) {
	mapFn, ok := args[0].(Function)
	if !ok {
		return nil, fmt.Errorf("expected query argument, received %T", args[0])
	}

	compareFn := func(ctx FunctionContext, values []interface{}, i, j int) (bool, error) {
		var leftValue, rightValue interface{}
		var err error

		if leftValue, err = mapFn.Exec(ctx.WithValue(values[i])); err != nil {
			return false, err
		}
		if rightValue, err = mapFn.Exec(ctx.WithValue(values[j])); err != nil {
			return false, err
		}

		switch leftValue.(type) {
		case float64, int, int64, uint64, json.Number:
			lhs, err := IGetNumber(leftValue)
			if err != nil {
				return false, fmt.Errorf("sort_by element %v: %w", i, ErrFrom(err, mapFn))
			}
			rhs, err := IGetNumber(rightValue)
			if err != nil {
				return false, fmt.Errorf("sort_by element %v: %w", j, ErrFrom(err, mapFn))
			}
			return lhs < rhs, nil
		case string, []byte:
			lhs, err := IGetString(leftValue)
			if err != nil {
				return false, fmt.Errorf("sort_by element %v: %w", i, ErrFrom(err, mapFn))
			}
			rhs, err := IGetString(rightValue)
			if err != nil {
				return false, fmt.Errorf("sort_by element %v: %w", j, ErrFrom(err, mapFn))
			}
			return lhs < rhs, nil
		}
		return false, fmt.Errorf("sort_by element %v: %w", i, ErrFrom(NewTypeError(leftValue, ValueNumber, ValueString), mapFn))
	}

	return ClosureFunction("method sort_by", func(ctx FunctionContext) (interface{}, error) {
		v, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}
		if m, ok := v.([]interface{}); ok {
			values := make([]interface{}, 0, len(m))
			values = append(values, m...)

			sort.Slice(values, func(i, j int) bool {
				if err == nil {
					var b bool
					b, err = compareFn(ctx, values, i, j)
					return b
				}
				return false
			})
			if err != nil {
				return nil, err
			}
			return values, nil
		}
		return nil, NewTypeErrorFrom(target.Annotation(), v, ValueArray)
	}, aggregateTargetPaths(target, mapFn)), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"slice", "",
	).InCategory(
		MethodCategoryStrings,
		"Extract a slice from a string by specifying two indices, a low and high bound, which selects a half-open range that includes the first character, but excludes the last one. If the second index is omitted then it defaults to the length of the input sequence.",
		NewExampleSpec("",
			`root.beginning = this.value.slice(0, 2)
root.end = this.value.slice(4)`,
			`{"value":"foo bar"}`,
			`{"beginning":"fo","end":"bar"}`,
		),
		NewExampleSpec(`A negative low index can be used, indicating an offset from the end of the sequence. If the low index is greater than the length of the sequence then an empty result is returned.`,
			`root.last_chunk = this.value.slice(-4)
root.the_rest = this.value.slice(0, -4)`,
			`{"value":"foo bar"}`,
			`{"last_chunk":" bar","the_rest":"foo"}`,
		),
	).InCategory(
		MethodCategoryObjectAndArray,
		"Extract a slice from an array by specifying two indices, a low and high bound, which selects a half-open range that includes the first element, but excludes the last one. If the second index is omitted then it defaults to the length of the input sequence.",
		NewExampleSpec("",
			`root.beginning = this.value.slice(0, 2)
root.end = this.value.slice(4)`,
			`{"value":["foo","bar","baz","buz","bev"]}`,
			`{"beginning":["foo","bar"],"end":["bev"]}`,
		),
		NewExampleSpec(
			`A negative low index can be used, indicating an offset from the end of the sequence. If the low index is greater than the length of the sequence then an empty result is returned.`,
			`root.last_chunk = this.value.slice(-2)
root.the_rest = this.value.slice(0, -2)`,
			`{"value":["foo","bar","baz","buz","bev"]}`,
			`{"last_chunk":["buz","bev"],"the_rest":["foo","bar","baz"]}`,
		),
	),
	sliceMethod,
	true,
	oldParamsExpectAtLeastOneArg(),
	oldParamsExpectIntArg(0),
	oldParamsExpectIntArg(1),
)

func sliceMethod(args ...interface{}) (simpleMethod, error) {
	start := args[0].(int64)
	var end *int64
	if len(args) > 1 {
		endV := args[1].(int64)
		end = &endV
		if endV > 0 && start >= endV {
			return nil, fmt.Errorf("lower slice bound %v must be lower than upper (%v)", start, endV)
		}
	}
	getBounds := func(l int64) (startV, endV int64, err error) {
		endV = l
		if end != nil {
			if *end < 0 {
				endV += *end
			} else {
				endV = *end
			}
		}
		if endV > l {
			endV = l
		}
		if endV < 0 {
			endV = 0
		}
		startV = start
		if startV < 0 {
			startV = l + startV
			if startV < 0 {
				startV = 0
			}
		}
		if startV > endV {
			err = fmt.Errorf("lower slice bound %v must be lower than or equal to upper bound (%v) and target length (%v)", startV, endV, l)
		}
		return
	}
	return func(v interface{}, ctx FunctionContext) (interface{}, error) {
		switch t := v.(type) {
		case string:
			start, end, err := getBounds(int64(len(t)))
			if err != nil {
				return nil, err
			}
			return t[start:end], nil
		case []byte:
			start, end, err := getBounds(int64(len(t)))
			if err != nil {
				return nil, err
			}
			return t[start:end], nil
		case []interface{}:
			start, end, err := getBounds(int64(len(t)))
			if err != nil {
				return nil, err
			}
			return t[start:end], nil
		}
		return nil, NewTypeError(v, ValueArray, ValueString)
	}, nil
}

//------------------------------------------------------------------------------

var _ = registerMethod(
	NewMethodSpec(
		"sum", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Sum the numerical values of an array.",
		NewExampleSpec("",
			`root.sum = this.foo.sum()`,
			`{"foo":[3,8,4]}`,
			`{"sum":15}`,
		),
	),
	sumMethod,
)

func sumMethod(target Function, _ *ParsedParams) (Function, error) {
	return ClosureFunction("method sum", func(ctx FunctionContext) (interface{}, error) {
		v, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}
		switch t := ISanitize(v).(type) {
		case float64, int64, uint64, json.Number:
			return v, nil
		case []interface{}:
			var total float64
			for i, v := range t {
				n, nErr := IGetNumber(v)
				if nErr != nil {
					err = fmt.Errorf("index %v: %w", i, nErr)
				} else {
					total += n
				}
			}
			if err != nil {
				return nil, err
			}
			return total, nil
		}
		return nil, NewTypeErrorFrom(target.Annotation(), v, ValueArray)
	}, target.QueryTargets), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"unique", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Attempts to remove duplicate values from an array. The array may contain a combination of different value types, but numbers and strings are checked separately (`\"5\"` is a different element to `5`).",
		NewExampleSpec("",
			`root.uniques = this.foo.unique()`,
			`{"foo":["a","b","a","c"]}`,
			`{"uniques":["a","b","c"]}`,
		),
	),
	uniqueMethod,
	false,
	oldParamsExpectOneOrZeroArgs(),
	oldParamsExpectFunctionArg(0),
)

func uniqueMethod(args ...interface{}) (simpleMethod, error) {
	var emitFn Function
	if len(args) > 0 {
		var ok bool
		emitFn, ok = args[0].(Function)
		if !ok {
			return nil, fmt.Errorf("expected query argument, received %T", args[0])
		}
	}

	return func(v interface{}, ctx FunctionContext) (interface{}, error) {
		slice, ok := v.([]interface{})
		if !ok {
			return nil, NewTypeError(v, ValueArray)
		}

		var strCompares map[string]struct{}
		var numCompares map[float64]struct{}

		checkStr := func(str string) bool {
			if strCompares == nil {
				strCompares = make(map[string]struct{}, len(slice))
			}
			_, exists := strCompares[str]
			if !exists {
				strCompares[str] = struct{}{}
			}
			return !exists
		}

		checkNum := func(num float64) bool {
			if numCompares == nil {
				numCompares = make(map[float64]struct{}, len(slice))
			}
			_, exists := numCompares[num]
			if !exists {
				numCompares[num] = struct{}{}
			}
			return !exists
		}

		uniqueSlice := make([]interface{}, 0, len(slice))
		for i, v := range slice {
			check := v
			if emitFn != nil {
				var err error
				if check, err = emitFn.Exec(ctx.WithValue(v)); err != nil {
					return nil, fmt.Errorf("index %v: %w", i, err)
				}
			}
			var unique bool
			switch t := ISanitize(check).(type) {
			case string:
				unique = checkStr(t)
			case []byte:
				unique = checkStr(string(t))
			case json.Number:
				f, err := t.Float64()
				if err != nil {
					var i int64
					if i, err = t.Int64(); err == nil {
						f = float64(i)
					}
				}
				if err != nil {
					return nil, fmt.Errorf("index %v: failed to parse number: %w", i, err)
				}
				unique = checkNum(f)
			case int64:
				unique = checkNum(float64(t))
			case uint64:
				unique = checkNum(float64(t))
			case float64:
				unique = checkNum(t)
			default:
				return nil, fmt.Errorf("index %v: %w", i, NewTypeError(check, ValueString, ValueNumber))
			}
			if unique {
				uniqueSlice = append(uniqueSlice, v)
			}
		}
		return uniqueSlice, nil
	}, nil
}

//------------------------------------------------------------------------------

var _ = registerSimpleMethod(
	NewMethodSpec(
		"values", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		"Returns the values of an object as an array. The order of the resulting array will be random.",
		NewExampleSpec("",
			`root.foo_vals = this.foo.values().sort()`,
			`{"foo":{"bar":1,"baz":2}}`,
			`{"foo_vals":[1,2]}`,
		),
	),
	func(*ParsedParams) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			if m, ok := v.(map[string]interface{}); ok {
				values := make([]interface{}, 0, len(m))
				for _, e := range m {
					values = append(values, e)
				}
				return values, nil
			}
			return nil, NewTypeError(v, ValueObject)
		}, nil
	},
)

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"without", "",
	).InCategory(
		MethodCategoryObjectAndArray,
		`Returns an object where one or more [field path][field_paths] arguments are removed. Each path specifies a specific field to be deleted from the input object, allowing for nested fields.

If a key within a nested path does not exist or is not an object then it is not removed.`,
		NewExampleSpec("",
			`root = this.without("inner.a","inner.c","d")`,
			`{"inner":{"a":"first","b":"second","c":"third"},"d":"fourth","e":"fifth"}`,
			`{"e":"fifth","inner":{"b":"second"}}`,
		),
	),
	func(args ...interface{}) (simpleMethod, error) {
		excludeList := make([][]string, 0, len(args))
		for _, arg := range args {
			excludeList = append(excludeList, gabs.DotPathToSlice(arg.(string)))
		}
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil, NewTypeError(v, ValueObject)
			}
			return mapWithout(m, excludeList), nil
		}, nil
	},
	true,
	oldParamsExpectAtLeastOneArg(),
	oldParamsExpectAllStringArgs(),
)

func mapWithout(m map[string]interface{}, paths [][]string) map[string]interface{} {
	newMap := make(map[string]interface{}, len(m))
	for k, v := range m {
		excluded := false
		var nestedExclude [][]string
		for _, p := range paths {
			if p[0] == k {
				if len(p) > 1 {
					nestedExclude = append(nestedExclude, p[1:])
				} else {
					excluded = true
				}
			}
		}
		if !excluded {
			if len(nestedExclude) > 0 {
				vMap, ok := v.(map[string]interface{})
				if ok {
					newMap[k] = mapWithout(vMap, nestedExclude)
				} else {
					newMap[k] = v
				}
			} else {
				newMap[k] = v
			}
		}
	}
	return newMap
}
