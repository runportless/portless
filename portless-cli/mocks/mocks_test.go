package mocks

import (
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestQueryMatchersKeepOperatorsAndRejectDuplicateNames(t *testing.T) {
	query, err := queryMatchers([]string{"warehouse=central", "include="}, []string{`sku=coffee-\w+=.*`})
	want := map[string]model.MockQueryMatcher{"warehouse": {Match: "equals", Value: "central"}, "include": {Match: "exists"}, "sku": {Match: "regex", Value: `coffee-\w+=.*`}}
	if err != nil || !reflect.DeepEqual(query, want) {
		t.Fatalf("query = %#v, %v", query, err)
	}
	for _, arguments := range [][]string{{"warehouse=.*"}, {"sku=coffee", "sku=tea"}, {"missing"}, {"=value"}} {
		if _, err := queryMatchers([]string{"warehouse=central"}, arguments); err == nil {
			t.Fatalf("accepted invalid query flags: %q", arguments)
		}
	}
}

func TestKeyValueValuesPreservesLiteralURLCharactersAndRepeatedValues(t *testing.T) {
	values, err := keyValueValues([]string{"query=a+b%20c", "query=second", "empty="}, "query")
	if err != nil {
		t.Fatal(err)
	}
	if len(values["query"]) != 2 || values["query"][0] != "a+b%20c" || values["query"][1] != "second" || len(values["empty"]) != 1 || values["empty"][0] != "" {
		t.Fatalf("values = %#v", values)
	}
}

func TestKeyValueMapRequiresNamedPairs(t *testing.T) {
	if _, err := keyValueMap([]string{"missing"}, "header"); err == nil {
		t.Fatal("missing separator was accepted")
	}
	if _, err := keyValueMap([]string{"=value"}, "query"); err == nil {
		t.Fatal("empty name was accepted")
	}
}
