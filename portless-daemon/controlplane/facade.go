package controlplane

import (
	"context"
	"net/http"

	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
)

// ServeIngress routes an application-host request without exposing the proxy
// implementation to HTTP transport packages.
func (s *Service) ServeIngress(writer http.ResponseWriter, request *http.Request, selector, service string) {
	s.proxy.ServeIngress(writer, request, selector, service)
}

// Subscribe exposes application events without exposing the broker itself.
func (s *Service) Subscribe(ctx context.Context, scope string, topics []string) events.Subscription {
	return s.broker.Subscribe(ctx, scope, topics)
}

// AddTrafficExchange records a captured exchange through the application boundary.
func (s *Service) AddTrafficExchange(exchange model.TrafficExchange) model.TrafficExchange {
	return s.traffic.AddExchange(exchange)
}
