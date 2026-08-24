"""FastAPI composition for the Dispatch operations service."""

import logging
import os
from contextlib import asynccontextmanager
from typing import Any

import httpx
from fastapi import FastAPI, Header, Query, Request
from fastapi.responses import JSONResponse

from .clients import DispatchClients, UpstreamError
from .domain import DomainError, trace_id
from .events import EventPublisher
from .models import DeliveryCreate
from .repository import DeliveryRepository

logging.basicConfig(level=logging.INFO, format="service=api %(message)s")
logger = logging.getLogger("dispatch-api")


@asynccontextmanager
async def lifespan(application: FastAPI):
    """Connect bounded runtime dependencies before accepting requests."""

    repository = DeliveryRepository(required_environment("DATABASE_URL"))
    publisher = EventPublisher(required_environment("NATS_URL"))
    http = httpx.AsyncClient(timeout=httpx.Timeout(5.0, connect=2.0))
    await repository.connect()
    try:
        await publisher.connect()
    except Exception:
        repository.close()
        await repository.wait_closed()
        await http.aclose()
        raise
    application.state.repository = repository
    application.state.publisher = publisher
    application.state.clients = DispatchClients(
        http,
        required_environment("GEOCODER_URL"),
        required_environment("ROUTING_URL"),
    )
    logger.info("event=ready")
    try:
        yield
    finally:
        await publisher.close()
        repository.close()
        await repository.wait_closed()
        await http.aclose()


app = FastAPI(title="Dispatch operations API", version="1.0.0", lifespan=lifespan)


@app.middleware("http")
async def identify_service(request: Request, call_next):
    """Attach an inspectable service identity to every response."""

    response = await call_next(request)
    response.headers["X-Dispatch-Service"] = "api"
    response.headers["X-Dispatch-Provider"] = "local"
    return response


@app.exception_handler(DomainError)
async def domain_error(_request: Request, error: DomainError) -> JSONResponse:
    return JSONResponse(
        status_code=error.status,
        content={"error": {"code": error.code, "message": error.message}},
    )


@app.exception_handler(UpstreamError)
async def upstream_error(_request: Request, error: UpstreamError) -> JSONResponse:
    payload = error.payload if isinstance(error.payload, dict) else {}
    detail = payload.get("error", {}) if isinstance(payload, dict) else {}
    message = detail.get("message") or str(error)
    return JSONResponse(
        status_code=error.status if 400 <= error.status <= 599 else 502,
        content={
            "error": {
                "code": detail.get("code", "DEPENDENCY_FAILED"),
                "message": message,
                "dependency": error.dependency,
            }
        },
    )


@app.get("/health")
async def health() -> dict[str, Any]:
    return {"service": "api", "ready": True, "provider": "local"}


@app.get("/locations")
async def locations(
    request: Request,
    query: str = Query(default="", max_length=80),
    traceparent: str | None = Header(default=None),
    tracestate: str | None = Header(default=None),
) -> dict[str, Any]:
    return await request.app.state.clients.locations(query, traceparent, tracestate)


@app.get("/estimates")
async def estimates(
    request: Request,
    pickup: str,
    destination: str,
    size: str,
    priority: str,
    traceparent: str | None = Header(default=None),
    tracestate: str | None = Header(default=None),
) -> dict[str, Any]:
    return await request.app.state.clients.estimate(
        pickup, destination, size, priority, traceparent, tracestate
    )


@app.get("/deliveries")
async def deliveries(request: Request) -> dict[str, Any]:
    return {"provider": "local", "deliveries": await request.app.state.repository.list()}


@app.get("/deliveries/{delivery_id}")
async def delivery(request: Request, delivery_id: str) -> dict[str, Any]:
    return await request.app.state.repository.get(delivery_id)


@app.post("/deliveries", status_code=201)
async def create_delivery(
    request: Request,
    value: DeliveryCreate,
    traceparent: str | None = Header(default=None),
    tracestate: str | None = Header(default=None),
) -> dict[str, Any]:
    estimate = await request.app.state.clients.estimate(
        value.pickup,
        value.destination,
        value.parcel_size,
        value.priority,
        traceparent,
        tracestate,
    )
    delivery = await request.app.state.repository.create(
        value.model_dump(), estimate
    )
    await request.app.state.publisher.publish(
        "delivery.created", delivery, trace_id(traceparent)
    )
    logger.info("event=created delivery_id=%s", delivery["id"])
    return delivery


@app.post("/deliveries/{delivery_id}/advance")
async def advance_delivery(
    request: Request,
    delivery_id: str,
    traceparent: str | None = Header(default=None),
) -> dict[str, Any]:
    delivery = await request.app.state.repository.advance(delivery_id)
    await request.app.state.publisher.publish(
        "delivery.status_changed", delivery, trace_id(traceparent)
    )
    logger.info(
        "event=status_changed delivery_id=%s status=%s",
        delivery["id"],
        delivery["status"],
    )
    return delivery


def required_environment(name: str) -> str:
    """Read one required Portless-provided runtime value."""

    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"{name} is required")
    return value

