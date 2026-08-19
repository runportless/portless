package mocks

import "testing"

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
