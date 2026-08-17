package traffic

import (
	"strings"
	"testing"
)

func TestTrafficUsesTailFlag(t *testing.T) {
	application, output, _ := newTestCommands(t)
	command := application.trafficCommand()
	list, _, err := command.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Flags().Lookup("follow") != nil {
		t.Fatal("traffic still exposes --follow")
	}
	if tail := list.Flags().Lookup("tail"); tail == nil || tail.Shorthand != "t" {
		t.Fatalf("traffic --tail flag = %#v, want shorthand -t", tail)
	}

	command.SetOut(output)
	command.SetUsageTemplate(application.UsageTemplate())
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Available Commands:", "list", "List captured application traffic"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("traffic help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestFaultDurationIsOptIn(t *testing.T) {
	application, output, _ := newTestCommands(t)
	command := application.faultCommand()
	add, _, err := command.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(output)
	command.SetUsageTemplate(application.UsageTemplate())
	if err := add.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--duration") || !strings.Contains(output.String(), "automatically disable") {
		t.Fatalf("fault help does not explain optional automatic disable:\n%s", output.String())
	}
	if strings.Contains(output.String(), "default 10m") {
		t.Fatalf("fault duration still defaults to ten minutes:\n%s", output.String())
	}
	durationFlag := add.Flags().Lookup("duration")
	if durationFlag == nil || durationFlag.DefValue != "0s" {
		t.Fatalf("duration default = %v, want 0s", durationFlag)
	}
}
