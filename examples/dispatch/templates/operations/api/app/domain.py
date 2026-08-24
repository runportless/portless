"""Pure delivery-domain rules."""

from dataclasses import dataclass


NEXT_STATUS = {
    "scheduled": "assigned",
    "assigned": "picked_up",
    "picked_up": "delivered",
}


@dataclass(slots=True)
class DomainError(Exception):
    """A stable error returned to an API caller."""

    code: str
    message: str
    status: int = 422

    def __str__(self) -> str:
        return self.message


def next_status(current: str) -> str:
    """Return the only legal next state for a delivery."""

    value = NEXT_STATUS.get(current)
    if value is None:
        raise DomainError(
            "INVALID_TRANSITION",
            f"Delivery in state {current} cannot advance",
            409,
        )
    return value


def trace_id(traceparent: str | None) -> str | None:
    """Extract the W3C trace identifier for an event or log field."""

    if not traceparent:
        return None
    parts = traceparent.strip().split("-")
    if len(parts) != 4 or len(parts[1]) != 32:
        return None
    return parts[1].lower()

