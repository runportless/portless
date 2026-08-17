package environment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/portless-run/portless/portless-cli/command"
)

func (c *Commands) printURL(ctx context.Context, requested string) error {
	_, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	name := requested
	if name == "" {
		name = environment.PrimaryService
	}
	for _, service := range environment.Services {
		if strings.EqualFold(service.Name, name) {
			endpoint := command.PrimaryServiceEndpoint(service)
			if endpoint == nil {
				return fmt.Errorf("service %s does not expose a public endpoint", service.Name)
			}
			if c.JSONOutput {
				return command.WriteJSON(c.Out, map[string]string{"service": service.Name, "url": endpoint.URL})
			}
			fmt.Fprintln(c.Out, endpoint.URL)
			return nil
		}
	}
	if name == "" {
		return errors.New("the environment has no primary HTTP service")
	}
	return fmt.Errorf("service %s was not found in %s/%s", name, environment.Project, environment.Name)
}
