package command

import "testing"

func TestRequiredArgumentCountUsesCommandSyntax(t *testing.T) {
	for use, expected := range map[string]int{
		"env select <project/environment>": 1,
		"project source add <name>":        1,
		"project source delete <name>":     1,
		"fault add <name> <source:target>": 2,
		"logs [service]":                   0,
		"doctor [daemon|relay|runtime]":    0,
		"runtime use <auto|docker|podman>": 1,
	} {
		if actual := requiredArgumentCount(use); actual != expected {
			t.Errorf("requiredArgumentCount(%q) = %d, want %d", use, actual, expected)
		}
	}
}
