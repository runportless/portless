package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-relay"
)

func TestRelayStatusContractMapsEveryRelayField(t *testing.T) {
	installedAt := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	status := relay.InstallationStatus{}
	statusValue := reflect.ValueOf(&status).Elem()
	for index := range statusValue.NumField() {
		field := statusValue.Field(index)
		switch field.Kind() {
		case reflect.String:
			field.SetString(statusValue.Type().Field(index).Name)
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Int:
			field.SetInt(int64(index + 1))
		case reflect.Pointer:
			if field.Type() != reflect.TypeOf((*time.Time)(nil)) {
				t.Fatalf("add test population for relay status field %s (%s)", statusValue.Type().Field(index).Name, field.Type())
			}
			field.Set(reflect.ValueOf(&installedAt))
		default:
			t.Fatalf("add test population for relay status field %s (%s)", statusValue.Type().Field(index).Name, field.Type())
		}
	}

	mapped := relayStatusContract(status)
	mappedValue := reflect.ValueOf(mapped)
	mappedType := reflect.TypeOf(contract.RelayStatus{})
	if statusValue.NumField() != mappedValue.NumField() {
		t.Fatalf("relay status has %d fields but API contract has %d", statusValue.NumField(), mappedValue.NumField())
	}
	for index := range statusValue.NumField() {
		sourceField := statusValue.Type().Field(index)
		targetField, found := mappedType.FieldByName(sourceField.Name)
		if !found {
			t.Errorf("relay API contract is missing field %s", sourceField.Name)
			continue
		}
		if targetField.Type != sourceField.Type || targetField.Tag.Get("json") != sourceField.Tag.Get("json") {
			t.Errorf("relay API field %s changed type or JSON contract", sourceField.Name)
			continue
		}
		if actual, expected := mappedValue.FieldByIndex(targetField.Index).Interface(), statusValue.Field(index).Interface(); !reflect.DeepEqual(actual, expected) {
			t.Errorf("relay API mapping omitted %s: got %#v, want %#v", sourceField.Name, actual, expected)
		}
	}
}
