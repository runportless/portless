package traffic

import (
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
)

func TestTCPApplicationTrafficUsesProtocolSpecificHumanOutput(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.printTrafficList(model.Environment{Project: "billing", Name: "local"}, "tcp", []model.TrafficEvent{{
		Sequence: 9, Protocol: model.ProtocolTCP, Source: "checkout", Target: "postgres", DurationMS: 4,
	}})
	for _, expected := range []string{"TCP traffic", "PROTOCOL", "TCP", "checkout:postgres", "ok"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("TCP traffic output does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "METHOD") || strings.Contains(output.String(), "CODE") {
		t.Fatalf("TCP traffic used HTTP columns:\n%s", output.String())
	}
}
