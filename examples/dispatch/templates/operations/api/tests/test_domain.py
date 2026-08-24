from datetime import UTC, datetime

import pytest

from app.clients import trace_headers
from app.domain import DomainError, next_status, trace_id
from app.repository import delivery_from_row, format_id, parse_id


def test_delivery_state_machine():
    assert next_status("scheduled") == "assigned"
    assert next_status("assigned") == "picked_up"
    assert next_status("picked_up") == "delivered"
    with pytest.raises(DomainError) as caught:
        next_status("delivered")
    assert caught.value.code == "INVALID_TRANSITION"
    assert caught.value.status == 409


def test_public_delivery_identity_and_mapping():
    assert format_id(7) == "D-0007"
    assert parse_id("D-0007") == 7
    with pytest.raises(DomainError):
        parse_id("opaque-7")

    value = delivery_from_row(
        {
            "id": 7,
            "pickup_code": "central-depot",
            "destination_code": "harbor",
            "parcel_size": "medium",
            "priority": "standard",
            "distance_meters": 7000,
            "eta_minutes": 17,
            "price_cents": 1540,
            "route_strategy": "standard",
            "status": "scheduled",
            "created_at": datetime(2026, 8, 23, 12, 0, tzinfo=UTC).replace(tzinfo=None),
            "updated_at": datetime(2026, 8, 23, 12, 0, tzinfo=UTC).replace(tzinfo=None),
        }
    )
    assert value["id"] == "D-0007"
    assert value["distanceKm"] == 7
    assert value["routeStrategy"] == "standard"


def test_trace_context_is_bounded_and_explicit():
    parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
    assert trace_headers(parent, "vendor=value") == {
        "traceparent": parent,
        "tracestate": "vendor=value",
    }
    assert trace_id(parent) == "4bf92f3577b34da6a3ce929d0e0e4736"
    assert trace_id("invalid") is None

