package query

import (
	"fmt"
	"regexp"
	"sort"
)

// FunctionSet contains an explicit set of functions to be available in a
// Bloblang query.
type FunctionSet struct {
	constructors map[string]FunctionCtor
	specs        map[string]FunctionSpec
}

// NewFunctionSet creates a function set without any functions in it.
func NewFunctionSet() *FunctionSet {
	return &FunctionSet{
		constructors: map[string]FunctionCtor{},
		specs:        map[string]FunctionSpec{},
	}
}

var nameRegexpRaw = `^[a-z0-9]+(_[a-z0-9]+)*$`
var nameRegexp = regexp.MustCompile(nameRegexpRaw)

// Add a new function to this set by providing a spec (name and documentation),
// a constructor to be called for each instantiation of the function, and
// information regarding the arguments of the function.
func (f *FunctionSet) Add(spec FunctionSpec, ctor FunctionCtor) error {
	if !nameRegexp.MatchString(spec.Name) {
		return fmt.Errorf("function name '%v' does not match the required regular expression /%v/", spec.Name, nameRegexpRaw)
	}
	if _, exists := f.constructors[spec.Name]; exists {
		return fmt.Errorf("conflicting function name: %v", spec.Name)
	}
	if err := spec.Params.validate(); err != nil {
		return err
	}
	f.constructors[spec.Name] = ctor
	f.specs[spec.Name] = spec
	return nil
}

// Docs returns a slice of function specs, which document each function.
func (f *FunctionSet) Docs() []FunctionSpec {
	specSlice := make([]FunctionSpec, 0, len(f.specs))
	for _, v := range f.specs {
		specSlice = append(specSlice, v)
	}
	sort.Slice(specSlice, func(i, j int) bool {
		return specSlice[i].Name < specSlice[j].Name
	})
	return specSlice
}

// List returns a slice of function names in alphabetical order.
func (f *FunctionSet) List() []string {
	functionNames := make([]string, 0, len(f.constructors))
	for k := range f.constructors {
		functionNames = append(functionNames, k)
	}
	sort.Strings(functionNames)
	return functionNames
}

// Params attempts to obtain an argument specification for a given function.
func (f *FunctionSet) Params(name string) (Params, error) {
	spec, exists := f.specs[name]
	if !exists {
		return OldStyleParams(), badFunctionErr(name)
	}
	return spec.Params, nil
}

// Init attempts to initialize a function of the set by name and zero or more
// arguments.
func (f *FunctionSet) Init(name string, args *ParsedParams) (Function, error) {
	ctor, exists := f.constructors[name]
	if !exists {
		return nil, badFunctionErr(name)
	}
	return wrapCtorWithDynamicArgs(name, args, ctor)
}

// Without creates a clone of the function set that can be mutated in isolation,
// where a variadic list of functions will be excluded from the set.
func (f *FunctionSet) Without(functions ...string) *FunctionSet {
	excludeMap := make(map[string]struct{}, len(functions))
	for _, k := range functions {
		excludeMap[k] = struct{}{}
	}

	constructors := make(map[string]FunctionCtor, len(f.constructors))
	for k, v := range f.constructors {
		if _, exists := excludeMap[k]; !exists {
			constructors[k] = v
		}
	}

	specs := map[string]FunctionSpec{}
	for _, v := range f.specs {
		if _, exists := excludeMap[v.Name]; !exists {
			specs[v.Name] = v
		}
	}
	return &FunctionSet{constructors, specs}
}

// OnlyPure creates a clone of the function set that can be mutated in
// isolation, where all impure functions are removed.
func (f *FunctionSet) OnlyPure() *FunctionSet {
	var excludes []string
	for _, v := range f.specs {
		if v.Impure {
			excludes = append(excludes, v.Name)
		}
	}
	return f.Without(excludes...)
}

// NoMessage creates a clone of the function set that can be mutated in
// isolation, where all message access functions are removed.
func (f *FunctionSet) NoMessage() *FunctionSet {
	var excludes []string
	for _, v := range f.specs {
		if v.Category == FunctionCategoryMessage {
			excludes = append(excludes, v.Name)
		}
	}
	return f.Without(excludes...)
}

//------------------------------------------------------------------------------

// AllFunctions is a set containing every single function declared by this
// package, and any globally declared plugin methods.
var AllFunctions = NewFunctionSet()

func registerFunction(spec FunctionSpec, ctor FunctionCtor) struct{} {
	if err := AllFunctions.Add(spec, func(args *ParsedParams) (Function, error) {
		return ctor(args)
	}); err != nil {
		panic(err)
	}
	return struct{}{}
}

func registerSimpleFunction(spec FunctionSpec, fn func(ctx FunctionContext) (interface{}, error)) struct{} {
	if err := AllFunctions.Add(spec, func(*ParsedParams) (Function, error) {
		return ClosureFunction("function "+spec.Name, fn, nil), nil
	}); err != nil {
		panic(err)
	}
	return struct{}{}
}

// InitFunctionHelper attempts to initialise a function by its name and a list
// of arguments, this is convenient for writing tests.
func InitFunctionHelper(name string, args ...interface{}) (Function, error) {
	spec, ok := AllFunctions.specs[name]
	if !ok {
		return nil, badFunctionErr(name)
	}
	parsedArgs, err := spec.Params.PopulateNameless(args...)
	if err != nil {
		return nil, err
	}
	return AllFunctions.Init(name, parsedArgs)
}

// FunctionDocs returns a slice of specs, one for each function.
func FunctionDocs() []FunctionSpec {
	return AllFunctions.Docs()
}

// ListFunctions returns a slice of function names, sorted alphabetically.
func ListFunctions() []string {
	return AllFunctions.List()
}

//------------------------------------------------------------------------------

func wrapCtorWithDynamicArgs(name string, args *ParsedParams, fn FunctionCtor) (Function, error) {
	fns := args.dynamic()
	if len(fns) == 0 {
		return fn(args)
	}
	return ClosureFunction("function "+name, func(ctx FunctionContext) (interface{}, error) {
		newArgs, err := args.ResolveDynamic(ctx)
		if err != nil {
			return nil, err
		}
		dynFunc, err := fn(newArgs)
		if err != nil {
			return nil, err
		}
		return dynFunc.Exec(ctx)
	}, aggregateTargetPaths(fns...)), nil
}
