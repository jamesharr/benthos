package query

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Jeffail/gabs/v2"
)

var _ = registerMethod(
	NewMethodSpec(
		"apply",
		"Apply a declared mapping to a target value.",
		NewExampleSpec("",
			`map thing {
  root.inner = this.first
}

root.foo = this.doc.apply("thing")`,
			`{"doc":{"first":"hello world"}}`,
			`{"foo":{"inner":"hello world"}}`,
		),
		NewExampleSpec("",
			`map create_foo {
  root.name = "a foo"
  root.purpose = "to be a foo"
}

root = this
root.foo = null.apply("create_foo")`,
			`{"id":"1234"}`,
			`{"foo":{"name":"a foo","purpose":"to be a foo"},"id":"1234"}`,
		),
	).Param(ParamString("mapping", "The mapping to apply.")),
	applyMethod,
)

func applyMethod(target Function, args *ParsedParams) (Function, error) {
	targetMap, err := args.FieldString("mapping")
	if err != nil {
		return nil, err
	}

	return ClosureFunction("map "+targetMap, func(ctx FunctionContext) (interface{}, error) {
		res, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}
		ctx = ctx.WithValue(res)

		if ctx.Maps == nil {
			return nil, errors.New("no maps were found")
		}
		m, ok := ctx.Maps[targetMap]
		if !ok {
			return nil, fmt.Errorf("map %v was not found", targetMap)
		}

		// ISOLATED VARIABLES
		ctx.Vars = map[string]interface{}{}
		return m.Exec(ctx)
	}, func(ctx TargetsContext) (TargetsContext, []TargetPath) {
		mapFn, ok := ctx.Maps[targetMap]
		if !ok {
			return target.QueryTargets(ctx)
		}

		mapCtx, targets := target.QueryTargets(ctx)
		mapCtx = mapCtx.WithValues(targets).WithValuesAsContext()

		returnCtx, mapTargets := mapFn.QueryTargets(mapCtx)
		return returnCtx, append(targets, mapTargets...)
	}), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec("bool", "").InCategory(
		MethodCategoryCoercion,
		"Attempt to parse a value into a boolean. An optional argument can be provided, in which case if the value cannot be parsed the argument will be returned instead. If the value is a number then any non-zero value will resolve to `true`, if the value is a string then any of the following values are considered valid: `1, t, T, TRUE, true, True, 0, f, F, FALSE`.",
		NewExampleSpec("",
			`root.foo = this.thing.bool()
root.bar = this.thing.bool(true)`,
		),
	),
	true, boolMethod,
	oldParamsExpectOneOrZeroArgs(),
	oldParamsExpectBoolArg(0),
)

func boolMethod(target Function, args ...interface{}) (Function, error) {
	defaultBool := false
	if len(args) > 0 {
		defaultBool = args[0].(bool)
	}
	return ClosureFunction("method bool", func(ctx FunctionContext) (interface{}, error) {
		v, err := target.Exec(ctx)
		if err != nil {
			if len(args) > 0 {
				return defaultBool, nil
			}
			return nil, err
		}
		f, err := IToBool(v)
		if err != nil {
			if len(args) > 0 {
				return defaultBool, nil
			}
			return nil, ErrFrom(err, target)
		}
		return f, nil
	}, target.QueryTargets), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"catch",
		"If the result of a target query fails (due to incorrect types, failed parsing, etc) the argument is returned instead.",
		NewExampleSpec("",
			`root.doc.id = this.thing.id.string().catch(uuid_v4())`,
		),
		NewExampleSpec("When the input document is not structured attempting to reference structured fields with `this` will result in an error. Therefore, a convenient way to delete non-structured data is with a catch.",
			`root = this.catch(deleted())`,
			`{"doc":{"foo":"bar"}}`,
			`{"doc":{"foo":"bar"}}`,
			`not structured data`,
			`<Message deleted>`,
		),
	),
	false, catchMethod,
	oldParamsExpectNArgs(1),
)

func catchMethod(fn Function, args ...interface{}) (Function, error) {
	catchFn, isFn := args[0].(Function)
	if !isFn {
		catchFn = NewLiteralFunction("", args[0])
	}
	return ClosureFunction("method catch", func(ctx FunctionContext) (interface{}, error) {
		res, err := fn.Exec(ctx)
		if err != nil {
			return catchFn.Exec(ctx)
		}
		return res, err
	}, aggregateTargetPaths(fn, catchFn)), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"from",
		"Modifies a target query such that certain functions are executed from the perspective of another message in the batch. This allows you to mutate events based on the contents of other messages. Functions that support this behaviour are `content`, `json` and `meta`.",
		NewExampleSpec("For example, the following map extracts the contents of the JSON field `foo` specifically from message index `1` of a batch, effectively overriding the field `foo` for all messages of a batch to that of message 1:",
			`root = this
root.foo = json("foo").from(1)`,
		),
	),
	false, func(target Function, args ...interface{}) (Function, error) {
		i64 := args[0].(int64)
		return &fromMethod{
			index:  int(i64),
			target: target,
		}, nil
	},
	oldParamsExpectNArgs(1),
	oldParamsExpectIntArg(0),
)

type fromMethod struct {
	index  int
	target Function
}

func (f *fromMethod) Annotation() string {
	return f.target.Annotation() + " from " + strconv.Itoa(f.index)
}

func (f *fromMethod) Exec(ctx FunctionContext) (interface{}, error) {
	ctx.Index = f.index
	return f.target.Exec(ctx)
}

func (f *fromMethod) QueryTargets(ctx TargetsContext) (TargetsContext, []TargetPath) {
	// TODO: Modify context to represent new index.
	return f.target.QueryTargets(ctx)
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"from_all",
		"Modifies a target query such that certain functions are executed from the perspective of each message in the batch, and returns the set of results as an array. Functions that support this behaviour are `content`, `json` and `meta`.",
		NewExampleSpec("",
			`root = this
root.foo_summed = json("foo").from_all().sum()`,
		),
	),
	false, fromAllMethod,
	oldParamsExpectNArgs(0),
)

func fromAllMethod(target Function, _ ...interface{}) (Function, error) {
	return ClosureFunction("method from_all", func(ctx FunctionContext) (interface{}, error) {
		values := make([]interface{}, ctx.MsgBatch.Len())
		var err error
		for i := 0; i < ctx.MsgBatch.Len(); i++ {
			subCtx := ctx
			subCtx.Index = i
			v, tmpErr := target.Exec(subCtx)
			if tmpErr != nil {
				if recovered, ok := tmpErr.(*ErrRecoverable); ok {
					values[i] = recovered.Recovered
				}
				err = tmpErr
			} else {
				values[i] = v
			}
		}
		if err != nil {
			return nil, &ErrRecoverable{
				Recovered: values,
				Err:       err,
			}
		}
		return values, nil
	}, target.QueryTargets), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"get",
		"Extract a field value, identified via a [dot path][field_paths], from an object.",
	).InCategory(
		MethodCategoryObjectAndArray, "",
		NewExampleSpec("",
			`root.result = this.foo.get(this.target)`,
			`{"foo":{"bar":"from bar","baz":"from baz"},"target":"bar"}`,
			`{"result":"from bar"}`,
			`{"foo":{"bar":"from bar","baz":"from baz"},"target":"baz"}`,
			`{"result":"from baz"}`,
		),
	),
	true, getMethodCtor,
	oldParamsExpectNArgs(1),
	oldParamsExpectStringArg(0),
)

type getMethod struct {
	fn   Function
	path []string
}

func (g *getMethod) Annotation() string {
	return "path `" + SliceToDotPath(g.path...) + "`"
}

func (g *getMethod) Exec(ctx FunctionContext) (interface{}, error) {
	v, err := g.fn.Exec(ctx)
	if err != nil {
		return nil, err
	}
	return gabs.Wrap(v).S(g.path...).Data(), nil
}

func (g *getMethod) QueryTargets(ctx TargetsContext) (TargetsContext, []TargetPath) {
	ctx, fnPaths := g.fn.QueryTargets(ctx)

	basePaths := ctx.Value()
	paths := make([]TargetPath, len(basePaths))
	for i, p := range basePaths {
		paths[i] = p
		paths[i].Path = append(paths[i].Path, g.path...)
	}
	ctx = ctx.WithValues(paths)

	return ctx, append(fnPaths, paths...)
}

// NewGetMethod creates a new get method.
func NewGetMethod(target Function, path string) (Function, error) {
	return getMethodCtor(target, path)
}

func getMethodCtor(target Function, args ...interface{}) (Function, error) {
	path := gabs.DotPathToSlice(args[0].(string))
	switch t := target.(type) {
	case *getMethod:
		newPath := append([]string{}, t.path...)
		newPath = append(newPath, path...)
		return &getMethod{
			fn:   t.fn,
			path: newPath,
		}, nil
	case *fieldFunction:
		return t.expand(path...), nil
	}
	return &getMethod{
		fn:   target,
		path: path,
	}, nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewHiddenMethodSpec("map"), false, mapMethod,
	oldParamsExpectNArgs(1),
	oldParamsExpectFunctionArg(0),
)

// NewMapMethod attempts to create a map method.
func NewMapMethod(target, mapArg Function) (Function, error) {
	return mapMethod(target, mapArg)
}

func mapMethod(target Function, args ...interface{}) (Function, error) {
	mapFn, ok := args[0].(Function)
	if !ok {
		return nil, fmt.Errorf("expected query argument, received %T", args[0])
	}
	return ClosureFunction(mapFn.Annotation(), func(ctx FunctionContext) (interface{}, error) {
		res, err := target.Exec(ctx)
		if err != nil {
			return nil, err
		}
		return mapFn.Exec(ctx.WithValue(res))
	}, func(ctx TargetsContext) (TargetsContext, []TargetPath) {
		mapCtx, targets := target.QueryTargets(ctx)
		mapCtx = mapCtx.WithValues(targets).WithValuesAsContext()

		returnCtx, mapTargets := mapFn.QueryTargets(mapCtx)
		return returnCtx, append(targets, mapTargets...)
	}), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewHiddenMethodSpec("not"), false, notMethodCtor,
	oldParamsExpectNArgs(0),
)

type notMethod struct {
	fn Function
}

// Not returns a logical NOT of a child function.
func Not(fn Function) Function {
	return &notMethod{
		fn: fn,
	}
}

func (n *notMethod) Annotation() string {
	return "not " + n.fn.Annotation()
}

func (n *notMethod) Exec(ctx FunctionContext) (interface{}, error) {
	v, err := n.fn.Exec(ctx)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, NewTypeErrorFrom(n.fn.Annotation(), v, ValueBool)
	}
	return !b, nil
}

func (n *notMethod) QueryTargets(ctx TargetsContext) (TargetsContext, []TargetPath) {
	return n.fn.QueryTargets(ctx)
}

func notMethodCtor(target Function, _ ...interface{}) (Function, error) {
	return &notMethod{fn: target}, nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"not_null", "",
	).InCategory(
		MethodCategoryCoercion,
		"Ensures that the given value is not `null`, and if so returns it, otherwise an error is returned.",
		NewExampleSpec("",
			`root.a = this.a.not_null()`,
			`{"a":"foobar","b":"barbaz"}`,
			`{"a":"foobar"}`,
			`{"b":"barbaz"}`,
			`Error("failed assignment (line 1): field `+"`this.a`"+`: value is null")`,
		),
	),
	func(...interface{}) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			if v == nil {
				return nil, errors.New("value is null")
			}
			return v, nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(0),
)

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"number", "",
	).InCategory(
		MethodCategoryCoercion,
		"Attempt to parse a value into a number. An optional argument can be provided, in which case if the value cannot be parsed into a number the argument will be returned instead.",
		NewExampleSpec("",
			`root.foo = this.thing.number() + 10
root.bar = this.thing.number(5) * 10`,
		),
	),
	true, numberCoerceMethod,
	oldParamsExpectOneOrZeroArgs(),
	oldParamsExpectFloatArg(0),
)

func numberCoerceMethod(target Function, args ...interface{}) (Function, error) {
	defaultNum := 0.0
	if len(args) > 0 {
		defaultNum = args[0].(float64)
	}
	return ClosureFunction("method number", func(ctx FunctionContext) (interface{}, error) {
		v, err := target.Exec(ctx)
		if err != nil {
			if len(args) > 0 {
				return defaultNum, nil
			}
			return nil, err
		}
		f, err := IToNumber(v)
		if err != nil {
			if len(args) > 0 {
				return defaultNum, nil
			}
			return nil, ErrFrom(err, target)
		}
		return f, nil
	}, target.QueryTargets), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsMethod(
	NewMethodSpec(
		"or", "If the result of the target query fails or resolves to `null`, returns the argument instead. This is an explicit method alternative to the coalesce pipe operator `|`.",
		NewExampleSpec("", `root.doc.id = this.thing.id.or(uuid_v4())`),
	),
	false, orMethod,
	oldParamsExpectNArgs(1),
)

func orMethod(fn Function, args ...interface{}) (Function, error) {
	orFn, isFn := args[0].(Function)
	if !isFn {
		orFn = NewLiteralFunction("", args[0])
	}
	return ClosureFunction("method or", func(ctx FunctionContext) (interface{}, error) {
		res, err := fn.Exec(ctx)
		if err != nil || IIsNull(res) {
			return orFn.Exec(ctx)
		}
		return res, err
	}, aggregateTargetPaths(fn, orFn)), nil
}

//------------------------------------------------------------------------------

var _ = registerOldParamsSimpleMethod(
	NewMethodSpec(
		"type", "",
	).InCategory(
		MethodCategoryCoercion,
		"Returns the type of a value as a string, providing one of the following values: `string`, `bytes`, `number`, `bool`, `array`, `object` or `null`.",
		NewExampleSpec("",
			`root.bar_type = this.bar.type()
root.foo_type = this.foo.type()`,
			`{"bar":10,"foo":"is a string"}`,
			`{"bar_type":"number","foo_type":"string"}`,
		),
	),
	func(...interface{}) (simpleMethod, error) {
		return func(v interface{}, ctx FunctionContext) (interface{}, error) {
			return string(ITypeOf(v)), nil
		}, nil
	},
	false,
	oldParamsExpectNArgs(0),
)
