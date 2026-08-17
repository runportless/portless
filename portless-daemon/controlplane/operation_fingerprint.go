package controlplane

import "encoding/json"

func operationFingerprint(operationType, service string, input any) string {
	payload := struct {
		Type    string `json:"type"`
		Service string `json:"service,omitempty"`
		Input   any    `json:"input,omitempty"`
	}{Type: operationType, Service: service, Input: input}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
