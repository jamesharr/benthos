package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMethodSetWithout(t *testing.T) {
	setOne := AllMethods
	setTwo := setOne.Without("explode")

	assert.Contains(t, setOne.List(), "explode")
	assert.NotContains(t, setTwo.List(), "explode")

	explodeParamSpec, _ := setOne.Params("explode")
	explodeParams, err := explodeParamSpec.PopulateNameless("foo.bar")
	require.NoError(t, err)

	_, err = setOne.Init("explode", NewLiteralFunction("", nil), explodeParams)
	assert.NoError(t, err)

	_, err = setTwo.Init("explode", NewLiteralFunction("", nil), explodeParams)
	assert.EqualError(t, err, "unrecognised method 'explode'")

	mapEachParamSpec, _ := setTwo.Params("map_each")
	mapEachParams, err := mapEachParamSpec.PopulateNameless(NewFieldFunction("foo"))
	require.NoError(t, err)

	_, err = setTwo.Init("map_each", NewLiteralFunction("", nil), mapEachParams)
	assert.NoError(t, err)
}

func TestMethodBadName(t *testing.T) {
	testCases := map[string]string{
		"!no":         "method name '!no' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"foo__bar":    "method name 'foo__bar' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"-foo-bar":    "method name '-foo-bar' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"foo-bar-":    "method name 'foo-bar-' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"":            "method name '' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"foo-bar":     "method name 'foo-bar' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"foo-bar_baz": "method name 'foo-bar_baz' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"FOO":         "method name 'FOO' does not match the required regular expression /^[a-z0-9]+(_[a-z0-9]+)*$/",
		"foobarbaz":   "",
		"foobarbaz89": "",
		"foo_bar_baz": "",
		"fo1_ba2_ba3": "",
	}

	for k, v := range testCases {
		t.Run(k, func(t *testing.T) {
			setOne := AllMethods.Without()
			err := setOne.Add(NewMethodSpec(k, ""), nil)
			if len(v) > 0 {
				assert.EqualError(t, err, v)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
