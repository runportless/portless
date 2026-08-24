"""Bounded clients for Portless-discovered HTTP dependencies."""

from dataclasses import dataclass
from typing import Any
from urllib.parse import urlencode

import httpx


@dataclass(slots=True)
class UpstreamError(Exception):
    """A dependency returned an error or unusable response."""

    dependency: str
    status: int
    payload: Any

    def __str__(self) -> str:
        return f"{self.dependency} returned HTTP {self.status}"


def trace_headers(traceparent: str | None, tracestate: str | None) -> dict[str, str]:
    """Copy only W3C propagation headers to an outbound request."""

    result: dict[str, str] = {}
    if traceparent:
        result["traceparent"] = traceparent
    if tracestate:
        result["tracestate"] = tracestate
    return result


class DispatchClients:
    """HTTP clients for geocoder and routing."""

    def __init__(self, client: httpx.AsyncClient, geocoder_url: str, routing_url: str):
        self.client = client
        self.geocoder_url = geocoder_url.rstrip("/")
        self.routing_url = routing_url.rstrip("/")

    async def locations(
        self, query: str, traceparent: str | None, tracestate: str | None
    ) -> dict[str, Any]:
        return await self._get(
            "geocoder",
            f"{self.geocoder_url}/locations?{urlencode({'query': query})}",
            traceparent,
            tracestate,
        )

    async def estimate(
        self,
        pickup: str,
        destination: str,
        size: str,
        priority: str,
        traceparent: str | None,
        tracestate: str | None,
    ) -> dict[str, Any]:
        query = urlencode(
            {
                "pickup": pickup,
                "destination": destination,
                "size": size,
                "priority": priority,
            }
        )
        return await self._get(
            "routing",
            f"{self.routing_url}/estimates?{query}",
            traceparent,
            tracestate,
        )

    async def _get(
        self,
        dependency: str,
        url: str,
        traceparent: str | None,
        tracestate: str | None,
    ) -> dict[str, Any]:
        try:
            response = await self.client.get(
                url, headers=trace_headers(traceparent, tracestate)
            )
        except httpx.HTTPError as error:
            raise UpstreamError(dependency, 503, {"message": str(error)}) from error
        try:
            payload = response.json()
        except ValueError as error:
            raise UpstreamError(
                dependency, response.status_code, {"message": "invalid JSON"}
            ) from error
        if not response.is_success:
            raise UpstreamError(dependency, response.status_code, payload)
        return payload

